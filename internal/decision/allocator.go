package decision

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fastiot/internal/ems/health"
	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/alloc"
)

const allocatorPointType = "easyemsAllocator"

// AllocatorConfig lives on a business node. A rule binds that node, not a copy
// of the member list, so adding a cabinet does not change the rule.
type AllocatorConfig struct {
	GraceMS   int `json:"grace_ms,omitempty"`
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// DeadbandKW suppresses re-dispatch when the next request is within this
	// many kW of the last one, so small fluctuations do not rewrite every PCS.
	DeadbandKW float64           `json:"deadband_kw,omitempty"`
	Members    []AllocatorMember `json:"members"`
}

// AllocatorMember is one PCS power target and its current capability sources.
type AllocatorMember struct {
	ID                 string  `json:"id"`
	Target             Target  `json:"target"`
	MinPower           float64 `json:"min_power,omitempty"`
	MaxCharge          float64 `json:"max_charge"`
	MaxDischarge       float64 `json:"max_discharge"`
	AvailableCharge    *Target `json:"available_charge,omitempty"`
	AvailableDischarge *Target `json:"available_discharge,omitempty"`
	Readback           *Target `json:"readback,omitempty"`
	Fault              *Target `json:"fault,omitempty"`
	// Priority fills higher-priority members to capacity before lower ones.
	Priority int `json:"priority,omitempty"`
	// Weight biases the proportional share (1 = default). It never raises a
	// member's hard capacity.
	Weight float64 `json:"weight,omitempty"`
	// Constraints derate or exclude this member when an input violates a limit.
	Constraints []MemberConstraint `json:"constraints,omitempty"`
}

func (c AllocatorConfig) grace() time.Duration {
	if c.GraceMS <= 0 {
		return 2 * time.Second
	}
	return time.Duration(c.GraceMS) * time.Millisecond
}

func (c AllocatorConfig) timeout() time.Duration {
	if c.TimeoutMS <= 0 {
		return 3 * time.Second
	}
	return time.Duration(c.TimeoutMS) * time.Millisecond
}

// LoadAllocator reads the current configuration from a business node.
func LoadAllocator(nc *nats.Conn, nodeID string) (AllocatorConfig, error) {
	nodes, err := siotclient.GetNodes(nc, "all", nodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return AllocatorConfig{}, fmt.Errorf("allocator unavailable")
	}
	point, found := nodes[0].Points.Find(allocatorPointType, "")
	if !found || point.Tombstone%2 != 0 || point.Txt() == "" {
		return AllocatorConfig{}, fmt.Errorf("allocator is not configured")
	}
	var config AllocatorConfig
	if err := json.Unmarshal([]byte(point.Txt()), &config); err != nil {
		return AllocatorConfig{}, fmt.Errorf("invalid allocator configuration")
	}
	if err := config.Validate(); err != nil {
		return AllocatorConfig{}, err
	}
	return config, nil
}

// Validate rejects ambiguous or incomplete configurations before they can run.
func (c AllocatorConfig) Validate() error {
	if len(c.Members) == 0 {
		return fmt.Errorf("allocator has no members")
	}
	if c.GraceMS < 0 || c.TimeoutMS < 0 {
		return fmt.Errorf("allocator timing must not be negative")
	}
	if !finite(c.DeadbandKW) || c.DeadbandKW < 0 {
		return fmt.Errorf("allocator deadband must be a non-negative number")
	}
	ids := map[string]bool{}
	targets := map[string]bool{}
	for _, member := range c.Members {
		if member.ID == "" {
			return fmt.Errorf("allocator member has no id")
		}
		if ids[member.ID] {
			return fmt.Errorf("duplicate allocator member %s", member.ID)
		}
		ids[member.ID] = true
		if member.Target.NodeID == "" {
			return fmt.Errorf("member %s has no power target", member.ID)
		}
		if targets[member.Target.NodeID] {
			return fmt.Errorf("member %s shares a power target with another member", member.ID)
		}
		targets[member.Target.NodeID] = true
		if member.MinPower < 0 || member.MaxCharge < 0 || member.MaxDischarge < 0 {
			return fmt.Errorf("member %s has negative power", member.ID)
		}
		if !finite(member.Weight) || member.Weight < 0 {
			return fmt.Errorf("member %s has an invalid weight", member.ID)
		}
		for name, target := range map[string]*Target{
			"available charge":    member.AvailableCharge,
			"available discharge": member.AvailableDischarge,
			"readback":            member.Readback,
			"fault":               member.Fault,
		} {
			if target != nil && target.NodeID == "" {
				return fmt.Errorf("member %s has no %s node", member.ID, name)
			}
		}
		for _, constraint := range member.Constraints {
			if err := constraint.validate(); err != nil {
				return fmt.Errorf("member %s: %w", member.ID, err)
			}
		}
	}
	return nil
}

// ValidateAllocatorScope checks every output and capability input against the
// allocator node's subtree. A member outside that subtree is rejected.
func ValidateAllocatorScope(nc *nats.Conn, parent string, config AllocatorConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	for _, member := range config.Members {
		if err := ValidateTarget(nc, parent, member.Target, true, nil); err != nil {
			return fmt.Errorf("member %s: %w", member.ID, err)
		}
		for _, target := range []*Target{member.AvailableCharge, member.AvailableDischarge, member.Fault, member.Readback} {
			if target != nil {
				if err := ValidateTarget(nc, parent, *target, false, nil); err != nil {
					return fmt.Errorf("member %s capability: %w", member.ID, err)
				}
			}
		}
		for _, constraint := range member.Constraints {
			if err := ValidateTarget(nc, parent, constraint.Target, false, nil); err != nil {
				return fmt.Errorf("member %s constraint %s: %w", member.ID, constraint.Kind, err)
			}
		}
	}
	return nil
}

// ValidateAllocationOwnership prevents two enabled tables from owning the same
// allocator. Direct writes to its members are handled as manual takeover.
func ValidateAllocationOwnership(nc *nats.Conn, ruleID, nodeID string) error {
	tables, err := siotclient.GetNodesType[DecisionTable](nc, "all", "all")
	if err != nil {
		return fmt.Errorf("cannot verify allocator ownership")
	}
	for _, table := range tables {
		if table.ID == ruleID || !table.Enabled || table.Applied == "" {
			continue
		}
		var document Document
		if json.Unmarshal([]byte(table.Applied), &document) != nil {
			continue
		}
		for _, row := range document.Table.Rows {
			for _, action := range row.Actions {
				if action.Notification != nil {
					continue
				}
				target := document.Bindings.Actions[action.ActionID]
				if target.NodeID == nodeID {
					return fmt.Errorf("allocator is already owned by %s", table.ID)
				}
			}
		}
	}
	return nil
}

// allocationMember resolves online state, manual takeover, current
// capabilities and constraint limits. The values are read again on every
// dispatch cycle. Member.Detail carries why the member was taken over,
// constrained or left at unknown health; it is empty when the member is
// healthy.
func allocationMember(nc *nats.Conn, member AllocatorMember) (alloc.Member, error) {
	// Online is never assumed: a member is eligible only when its health is
	// positively known, so an unobserved device cannot lend capacity.
	result := alloc.Member{
		ID:       member.ID,
		MinPower: member.MinPower, MaxCharge: member.MaxCharge, MaxDischarge: member.MaxDischarge,
		AvailableCharge: member.MaxCharge, AvailableDischarge: member.MaxDischarge,
		Priority: member.Priority, Weight: member.Weight,
	}
	var reasons []string
	nodes, err := siotclient.GetNodes(nc, "all", member.Target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return result, fmt.Errorf("member %s target unavailable", member.ID)
	}
	node := nodes[0]
	if disabled, _ := node.Points.Value(data.PointTypeDisabled, ""); disabled != 0 {
		result.Disabled = true
	}
	if manual, _ := node.Points.Value("easyemsManual", ""); manual != 0 {
		result.Disabled = true
		reasons = append(reasons, "manual takeover")
	}
	state, _ := node.Points.Text("health", "")
	switch {
	case state != "":
		result.Online = state != "offline" && state != "unknown"
		if state == "unknown" {
			reasons = append(reasons, "health unknown")
		}
	case node.Type == data.NodeTypeModbusIO:
		// The bridge publishes one health point per device point
		// (bridge.PublishDeviceHealth). A device point without one has not been
		// observed yet, so its state is unknown, not online. A bus heartbeat is
		// not device health and does not stand in for it here.
		reasons = append(reasons, "health unknown")
	default:
		// Variables and other non-device nodes are configuration or virtual
		// objects: no adapter publishes health for them, so there is no device
		// state that could be unknown. Their configured capability is the whole
		// truth and they stay eligible.
		result.Online = true
	}
	if member.Fault != nil {
		fault, found, err := readNumber(nc, *member.Fault)
		if err != nil {
			return result, err
		}
		if !found || fault != 0 {
			result.Faulted = true
		}
	}
	if member.AvailableDischarge != nil {
		value, found, err := readNumber(nc, *member.AvailableDischarge)
		if err != nil {
			return result, err
		}
		if !found {
			if result.Online && !result.Faulted {
				result.NotReady = true
			}
			result.Faulted = true
			result.AvailableDischarge = 0
		} else {
			result.AvailableDischarge = value
		}
	}
	if member.AvailableCharge != nil {
		value, found, err := readNumber(nc, *member.AvailableCharge)
		if err != nil {
			return result, err
		}
		if !found {
			if result.Online && !result.Faulted {
				result.NotReady = true
			}
			result.Faulted = true
			result.AvailableCharge = 0
		} else {
			result.AvailableCharge = value
		}
	}
	for _, violation := range EvaluateConstraints(member.Constraints, func(target Target) (float64, bool) {
		value, found, err := readNumber(nc, target)
		if err != nil {
			return 0, false
		}
		return value, found
	}) {
		reasons = append(reasons, violation.Reason)
		switch violation.Direction {
		case "charge":
			result.AvailableCharge = 0
		case "discharge":
			result.AvailableDischarge = 0
		case "both":
			result.Disabled = true
		}
	}
	result.Detail = strings.Join(reasons, "; ")
	return result, nil
}

func busHealthy(nc *nats.Conn, busID string) bool {
	nodes, err := siotclient.GetNodes(nc, "all", busID, "", false)
	if err != nil || len(nodes) == 0 {
		return false
	}
	heartbeat, ok := nodes[0].Points.Find("lastGoodPoll", "")
	lastSuccess := time.Time{}
	if ok {
		lastSuccess = time.Unix(int64(heartbeat.Val()), 0)
	}
	poll, _ := nodes[0].Points.Value(data.PointTypePollPeriod, "")
	timeout, _ := nodes[0].Points.Value(data.PointTypeTimeout, "")
	createdAt := health.CreatedAt(nodes[0].Points)
	verdict := health.Evaluate(health.Input{
		Requests: 1, ConsecutiveFailures: 1, LastSuccess: lastSuccess,
		PollPeriod: time.Duration(poll) * time.Millisecond,
		Timeout:    time.Duration(timeout) * time.Millisecond,
		CreatedAt:  createdAt,
	}, time.Now())
	return verdict.State == health.Healthy || verdict.State == health.Degraded
}

func readNumber(nc *nats.Conn, target Target) (float64, bool, error) {
	nodes, err := siotclient.GetNodes(nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return 0, false, fmt.Errorf("capability input unavailable")
	}
	if nodes[0].Type == data.NodeTypeModbusIO {
		if health, _ := nodes[0].Points.Text("health", ""); health == "offline" || health == "unknown" {
			return 0, false, nil
		} else if health == "" && !busHealthy(nc, nodes[0].Parent) {
			return 0, false, nil
		}
	}
	pointType := target.PointType
	if pointType == "" {
		pointType = data.PointTypeValue
	}
	point, found := nodes[0].Points.Find(pointType, target.Key)
	if !found || point.Tombstone%2 != 0 {
		return 0, false, nil
	}
	value, err := point.ValueFloat()
	if err != nil {
		return 0, false, nil
	}
	return value, true, nil
}
