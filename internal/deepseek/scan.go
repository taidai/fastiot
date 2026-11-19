package deepseek

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	kindIP         = "ip_address"
	kindPhone      = "phone"
	kindAccount    = "account"
	kindCredential = "credential"
	kindSerial     = "serial"
	kindAddress    = "address"
)

var kindLabels = map[string]string{
	kindIP:         "IP 地址",
	kindPhone:      "电话",
	kindAccount:    "账号",
	kindCredential: "凭据",
	kindSerial:     "序列号",
	kindAddress:    "地址",
}

type textHit struct {
	start int
	end   int
	kind  string
}

type simplePattern struct {
	kind string
	re   *regexp.Regexp
}

var (
	ipv4Pattern      = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	ipv6TokenPattern = regexp.MustCompile(`[0-9a-fA-F:]{7,}`)

	simplePatterns = []simplePattern{
		{kindPhone, regexp.MustCompile(`\b(?:1[3-9][0-9]{9}|0[0-9]{2,3}-[0-9]{7,8})\b`)},
		{kindAccount, regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)},
		{kindCredential, regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|secret|token|api[\s_-]?key|access[\s_-]?key|client[\s_-]?secret|bearer)\b\s*[:=：]\s*\S+`)},
		{kindCredential, regexp.MustCompile(`(?i)\bauthorization\b\s*[:=]\s*\S+`)},
		{kindCredential, regexp.MustCompile(`(?:密码|口令|密钥|令牌|凭据|账号|账户)\s*[:=：]\s*\S+`)},
		{kindCredential, regexp.MustCompile(`(?i)\bsk-[a-z0-9]{16,}\b`)},
		{kindSerial, regexp.MustCompile(`(?i)\b(?:s/n|sn|serial(?:\s*(?:no\.?|number))?)\b\s*[:#：]?\s*[a-z0-9][a-z0-9-]{5,}`)},
		{kindAddress, regexp.MustCompile(`[\p{Han}]{2,12}(?:省|市)[\p{Han}]{2,12}(?:区|县|市|镇|乡|街道)|[\p{Han}]{2,12}(?:路|街)[0-9]{1,5}号|[\p{Han}]{2,12}(?:小区|大厦|园区|广场)`)},
	}
)

// ScanText locates suspected sensitive content in one outbound field. It
// returns the position so the caller can point at the exact place; the
// preview masks the match so the finding itself does not repeat the secret.
func ScanText(category string, item int, field, text string) []Finding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	hits := []textHit{}
	for _, location := range ipv4Pattern.FindAllStringIndex(text, -1) {
		if ip := net.ParseIP(text[location[0]:location[1]]); ip != nil && ip.To4() != nil {
			hits = append(hits, textHit{location[0], location[1], kindIP})
		}
	}
	for _, location := range ipv6TokenPattern.FindAllStringIndex(text, -1) {
		token := text[location[0]:location[1]]
		if strings.Contains(token, ":") {
			if ip := net.ParseIP(token); ip != nil && ip.To4() == nil {
				hits = append(hits, textHit{location[0], location[1], kindIP})
			}
		}
	}
	hits = append(hits, patternHits(text, simplePatterns)...)
	return findingsFromHits(category, item, field, text, hits)
}

// ScanCredentials reports credential-shaped content only. A project artifact
// is scanned for secrets, not for the addresses, phone numbers and accounts
// that decide what may leave for the model.
func ScanCredentials(category, field, text string) []Finding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	patterns := make([]simplePattern, 0, len(simplePatterns))
	for _, pattern := range simplePatterns {
		if pattern.kind == kindCredential {
			patterns = append(patterns, pattern)
		}
	}
	return findingsFromHits(category, 0, field, text, patternHits(text, patterns))
}

func patternHits(text string, patterns []simplePattern) []textHit {
	hits := []textHit{}
	for _, pattern := range patterns {
		for _, location := range pattern.re.FindAllStringIndex(text, -1) {
			hits = append(hits, textHit{location[0], location[1], pattern.kind})
		}
	}
	return hits
}

func findingsFromHits(category string, item int, field, text string, hits []textHit) []Finding {
	if len(hits) == 0 {
		return nil
	}
	sort.SliceStable(hits, func(left, right int) bool {
		if hits[left].start == hits[right].start {
			return hits[left].end > hits[right].end
		}
		return hits[left].start < hits[right].start
	})
	kept := make([]textHit, 0, len(hits))
	for _, hit := range hits {
		overlaps := false
		for _, existing := range kept {
			if hit.start < existing.end && hit.end > existing.start {
				overlaps = true
				break
			}
		}
		if !overlaps {
			kept = append(kept, hit)
		}
	}
	findings := make([]Finding, 0, len(kept))
	for _, hit := range kept {
		position := utf8.RuneCountInString(text[:hit.start])
		findings = append(findings, Finding{
			ID:       fmt.Sprintf("%s:%d:%s:%d:%s", category, item, field, position, hit.kind),
			Category: category,
			Item:     item,
			Field:    field,
			Kind:     hit.kind,
			Position: position,
			Line:     strings.Count(text[:hit.start], "\n") + 1,
			Preview:  kindLabels[hit.kind] + " " + maskSecret(text[hit.start:hit.end]),
			Blocking: true,
		})
	}
	return findings
}

func maskSecret(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	if len(runes) <= 2 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1])
}
