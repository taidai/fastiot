package deepseek

import (
	"strings"
	"testing"
)

// TestScanCredentialsFindsSecretsButNotAddresses keeps the package scan narrow:
// a project artifact must be refused for carrying a credential, not for naming
// an address or a phone number that a screen may legitimately show.
func TestScanCredentialsFindsSecretsButNotAddresses(t *testing.T) {
	for _, text := range []string{
		`const apiKey = "sk-live-0123456789abcdef";`,
		`const token = "abc123";`,
		`密码：123456`,
		"Authorization: Bearer abc.def",
	} {
		if findings := ScanCredentials("screen", "source", text); len(findings) == 0 {
			t.Fatalf("credential not found in %q", text)
		} else if !findings[0].Blocking {
			t.Fatalf("credential finding is not blocking: %#v", findings[0])
		}
	}
	for _, text := range []string{
		`<span>192.168.1.10</span>`,
		`<p>现场：杭州市西湖区文三路 100 号</p>`,
		`<a href="tel:13800138000">联系</a>`,
		`<span data-fastiot-point="level">--</span>`,
	} {
		if findings := ScanCredentials("screen", "source", text); len(findings) != 0 {
			t.Fatalf("non-credential text refused: %q %#v", text, findings)
		}
	}
}

// TestScanCredentialsMasksTheMatch keeps the finding from repeating the secret
// it reports.
func TestScanCredentialsMasksTheMatch(t *testing.T) {
	findings := ScanCredentials("screen", "source", `const apiKey = "sk-live-0123456789abcdef";`)
	if len(findings) == 0 {
		t.Fatal("credential not found")
	}
	if finding := findings[0]; finding.Preview == "" || strings.Contains(finding.Preview, "0123456789abcdef") {
		t.Fatalf("finding repeats the secret: %#v", finding)
	}
}
