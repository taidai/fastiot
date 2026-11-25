package decision

import (
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
)

type outputTarget struct {
	nodeID    string
	pointType string
	key       string
}

func normalizeOutput(target Target) outputTarget {
	pointType := target.PointType
	if pointType == "" {
		pointType = "valueSet"
	}
	key := target.Key
	if key == "0" {
		key = ""
	}
	return outputTarget{nodeID: target.NodeID, pointType: pointType, key: key}
}

func outputTargets(document Document) []outputTarget {
	var targets []outputTarget
	for _, row := range document.Table.Rows {
		for _, action := range row.Actions {
			target := document.Bindings.Actions[action.ActionID]
			if target.NodeID == "" {
				continue
			}
			normalized := normalizeOutput(target)
			duplicate := false
			for _, existing := range targets {
				if existing == normalized {
					duplicate = true
					break
				}
			}
			if !duplicate {
				targets = append(targets, normalized)
			}
		}
	}
	return targets
}

// ValidateOutputOwnership enforces the v1 rule that one controllable point can
// be owned by only one enabled decision table at a time. The check runs on
// enable and on republish of an already enabled table, so ownership cannot be
// changed by publishing a new version behind the operator's back.
func ValidateOutputOwnership(nc *nats.Conn, ruleID string, document Document) error {
	return ValidateResolvedOutputOwnership(nc, ruleID, document, nil)
}

// ValidateResolvedOutputOwnership includes the complete declared command write
// set supplied by the application's Profile resolver, without another evaluator.
func ValidateResolvedOutputOwnership(nc *nats.Conn, ruleID string, document Document, resolve func(Document) ([]Target, error)) error {
	targets := func(doc Document) ([]outputTarget, error) {
		if resolve == nil {
			return outputTargets(doc), nil
		}
		raw, err := resolve(doc)
		if err != nil {
			return nil, err
		}
		result := []outputTarget{}
		seen := map[outputTarget]bool{}
		for _, target := range raw {
			normalized := normalizeOutput(target)
			if !seen[normalized] {
				seen[normalized] = true
				result = append(result, normalized)
			}
		}
		return result, nil
	}
	tables, err := siotclient.GetNodesType[DecisionTable](nc, "all", "all")
	if err != nil {
		return fmt.Errorf("cannot verify output ownership")
	}
	mine, err := targets(document)
	if err != nil {
		return err
	}
	if len(mine) == 0 {
		return nil
	}
	for _, table := range tables {
		if table.ID == ruleID || !table.Enabled || table.Applied == "" {
			continue
		}
		var other Document
		if json.Unmarshal([]byte(table.Applied), &other) != nil {
			continue
		}
		otherTargets, err := targets(other)
		if err != nil {
			return fmt.Errorf("cannot resolve active output ownership: %w", err)
		}
		for _, owned := range otherTargets {
			for _, target := range mine {
				if owned == target {
					name := table.Description
					if name == "" {
						name = table.ID
					}
					return fmt.Errorf("output %s is already owned by %s", target.nodeID, name)
				}
			}
		}
	}
	return nil
}
