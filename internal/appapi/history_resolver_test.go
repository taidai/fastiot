package appapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"fastiot/internal/ems/historyquery"
	"github.com/simpleiot/simpleiot/data"
)

func TestHistoryResolverDistinguishesBackendFailureFromDenial(t *testing.T) {
	backendErr := errors.New("nats: no responders")
	tests := []struct {
		name string
		load func(string) ([]data.NodeEdge, error)
		want error
	}{
		{
			name: "backend failure is unavailable",
			load: func(string) ([]data.NodeEdge, error) { return nil, backendErr },
			want: historyquery.ErrUnavailable,
		},
		{
			name: "missing node is forbidden",
			load: func(string) ([]data.NodeEdge, error) { return nil, nil },
			want: historyquery.ErrForbidden,
		},
		{
			name: "unsupported node type",
			load: func(string) ([]data.NodeEdge, error) {
				return []data.NodeEdge{{ID: "group", Type: "group"}}, nil
			},
			want: historyquery.ErrNotSupported,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := historyResolver{nodes: test.load}
			if _, err := resolver.Resolve("node"); !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want %v", err, test.want)
			}
		})
	}
}

func TestHistoryResolverReturnsSupportedNode(t *testing.T) {
	resolver := historyResolver{nodes: func(string) ([]data.NodeEdge, error) {
		return []data.NodeEdge{{
			ID: "point", Type: data.NodeTypeVariable,
			Points: data.Points{data.NewPointString(data.PointTypeUnits, "0", "kW")},
		}}, nil
	}}
	node, err := resolver.Resolve("point")
	if err != nil {
		t.Fatal(err)
	}
	if node.ID != "point" || node.Type != data.NodeTypeVariable || node.Units != "kW" {
		t.Fatalf("resolved node = %+v", node)
	}
}

func TestWriteHistoryErrorStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"backend unavailable is retryable", historyquery.ErrUnavailable, http.StatusServiceUnavailable},
		{"denied stays forbidden", historyquery.ErrForbidden, http.StatusForbidden},
		{"unsupported is a client error", historyquery.ErrNotSupported, http.StatusBadRequest},
		{"unknown failure is unavailable", errors.New("boom"), http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeHistoryError(recorder, test.err)
			if recorder.Code != test.want {
				t.Fatalf("status=%d, want %d", recorder.Code, test.want)
			}
		})
	}
}
