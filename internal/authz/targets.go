package authz

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type targetKind int

const (
	targetPath targetKind = iota
	targetQuery
	targetBody
	targetAlarm
)

type targetSpec struct {
	kind   targetKind
	key    string
	index  int
	status int
	reason string
}

func newTarget(spec targetSpec) targetSpec {
	if spec.status == 0 {
		spec.status = http.StatusForbidden
		spec.reason = "forbidden"
	}
	return spec
}

func (r routeRequirement) pathTarget(index int) routeRequirement {
	r.targets = append(r.targets, newTarget(targetSpec{kind: targetPath, index: index}))
	return r
}

func (r routeRequirement) alarmTarget(index int) routeRequirement {
	r.targets = append(r.targets, newTarget(targetSpec{kind: targetAlarm, index: index}))
	return r
}

func (r routeRequirement) queryTarget(name string) routeRequirement {
	r.targets = append(r.targets, newTarget(targetSpec{kind: targetQuery, key: name}))
	return r
}

func (r routeRequirement) queryTargetError(name string, status int, reason string) routeRequirement {
	r.targets = append(r.targets, newTarget(targetSpec{kind: targetQuery, key: name, status: status, reason: reason}))
	return r
}

func (r routeRequirement) bodyTarget(name string) routeRequirement {
	r.targets = append(r.targets, newTarget(targetSpec{kind: targetBody, key: name}))
	return r
}

func (r routeRequirement) bodyTargetError(name string, status int, reason string) routeRequirement {
	r.targets = append(r.targets, newTarget(targetSpec{kind: targetBody, key: name, status: status, reason: reason}))
	return r
}

// Target is one object whose visibility must be checked before dispatch.
type Target struct {
	ID     string
	Alarm  bool
	Status int
	Reason string
}

func requestJSONFields(r *http.Request) (map[string]json.RawMessage, bool) {
	if r.Body == nil {
		return nil, false
	}
	original := r.Body
	prefix, _ := io.ReadAll(io.LimitReader(original, 1<<20))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(prefix), original), original}
	decoder := json.NewDecoder(bytes.NewReader(prefix))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, ok := token.(string)
		if !ok {
			return nil, false
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return nil, false
		}
		// encoding/json matches struct fields case-insensitively and keeps the
		// last duplicate; scope extraction must use the same rules.
		fields[strings.ToLower(key)] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return fields, true
}

func targetStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return nil
	}
	out := many[:0]
	for _, id := range many {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func resolveTargets(r *http.Request, route routeRequirement) ([]Target, *Target) {
	path := strings.TrimPrefix(strings.Trim(r.URL.Path, "/"), "api/")
	parts := strings.Split(path, "/")
	var fields map[string]json.RawMessage
	bodyLoaded := false
	targets := []Target{}
	for _, spec := range route.targets {
		ids := []string{}
		switch spec.kind {
		case targetPath, targetAlarm:
			if spec.index < len(parts) && parts[spec.index] != "" {
				ids = []string{parts[spec.index]}
			}
		case targetQuery:
			ids = r.URL.Query()[spec.key]
		case targetBody:
			if !bodyLoaded {
				var valid bool
				fields, valid = requestJSONFields(r)
				bodyLoaded = true
				if !valid {
					return nil, &Target{Status: spec.status, Reason: spec.reason}
				}
			}
			ids = targetStrings(fields[spec.key])
		}
		for _, id := range ids {
			if id != "" {
				targets = append(targets, Target{ID: id, Alarm: spec.kind == targetAlarm, Status: spec.status, Reason: spec.reason})
			}
		}
	}
	return targets, nil
}
