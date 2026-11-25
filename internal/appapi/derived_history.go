package appapi

import (
	"encoding/json"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/historyquery"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Persist changes through the existing bounded SIOT history, never on GET.
// Retaining the latest observation does not promise complete retained history.
func (h *Handler) persistDerivedInputs() error {
	for _, typ := range []string{virtualInputType, calculationType} {
		nodes, err := client.GetNodes(h.nc, "all", "all", typ, false)
		if err != nil {
			return err
		}
		for _, node := range nodes {
			input := h.readBoundInput(decision.Target{NodeID: node.ID, Use: "decision"}, time.Now())
			if input == nil {
				continue
			}
			var previous decision.InputObservation
			if point, ok := node.Points.Find(historyquery.DerivedObservationPoint, ""); ok && point.Tombstone%2 == 0 && json.Unmarshal([]byte(point.Txt()), &previous) == nil {
				comparison := *input
				comparison.EvaluatedAt = previous.EvaluatedAt
				// Stored timestamps have no process-local monotonic component.
				// Compare the persisted representation, not Go's time internals.
				encoded, err := json.Marshal(comparison)
				if err == nil && string(encoded) == point.Txt() {
					continue
				}
			}
			if input.EvaluatedAt.IsZero() {
				input.EvaluatedAt = time.Now()
			}
			encoded, err := json.Marshal(input)
			if err != nil {
				return err
			}
			point := data.NewPointString(historyquery.DerivedObservationPoint, "0", string(encoded))
			if err := client.SendNodePoint(h.nc, node.ID, point, true); err != nil {
				return err
			}
		}
	}
	return nil
}
