package decision

import (
	"fmt"
	"math"

	"github.com/nats-io/nats.go"

	"fastiot/internal/ems/alloc"
)

// withinDeadband reports whether the next request is close enough to the last
// dispatched request that re-dispatching would only add write noise.
func withinDeadband(last, next, deadband float64) bool {
	return deadband > 0 && math.Abs(next-last) <= deadband
}

// SimulateAllocation computes what the allocator would do for a request without
// writing to any member. It reads current state only, so the result is the
// distribution that a real dispatch would produce right now.
func SimulateAllocation(nc *nats.Conn, config AllocatorConfig, request float64) (alloc.Result, error) {
	if err := config.Validate(); err != nil {
		return alloc.Result{}, err
	}
	if math.IsNaN(request) || math.IsInf(request, 0) {
		return alloc.Result{}, fmt.Errorf("request must be finite")
	}
	members := make([]alloc.Member, 0, len(config.Members))
	for _, cfg := range config.Members {
		member, err := allocationMember(nc, cfg)
		if err != nil {
			return alloc.Result{}, err
		}
		members = append(members, member)
	}
	return alloc.Allocate(request, members)
}
