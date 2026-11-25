package appapi

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"fastiot/internal/notification"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func (h *Handler) notificationStore() (*notification.Store, error) {
	h.notificationMu.Lock()
	defer h.notificationMu.Unlock()
	if h.notifications == nil {
		store, err := notification.Open(h.dataDir)
		if err != nil {
			return nil, err
		}
		h.notifications = store
	}
	return h.notifications, nil
}
func (h *Handler) notificationScopeExists(id string) bool {
	if id == "" {
		return false
	}
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	return err == nil && len(nodes) > 0 && nodes[0].Type != data.NodeTypeUser
}
func (h *Handler) canReadNotification(identity Identity, scope string) bool {
	return h.allows(identity, scope) && h.notificationScopeExists(scope)
}

// ValidateNotificationRecipients is actor-independent. The rule publisher/runtime
// must enforce configure/publish/project execution authority separately. No roster
// or role implies selection: every explicit account must currently read the scope.
func (h *Handler) ValidateNotificationRecipients(scope string, ids []string) error {
	if len(ids) == 0 || len(ids) > 100 {
		return notification.ErrInvalid
	}
	if !h.notificationScopeExists(scope) {
		return notification.ErrForbidden
	}
	for _, id := range ids {
		subject, active := h.identityForUser(id)
		if !active || !h.allows(subject, scope) {
			return notification.ErrForbidden
		}
	}
	return nil
}

// CreateRuleNotification is a trusted internal producer, not an HTTP endpoint.
// Caller verifies enabled/applied revision, durable execution reservation and
// execution authorization, and supplies the persisted rule mount as ScopeNodeID.
// Only a durable record is acknowledged; never delivery, reading or device state.
func (h *Handler) CreateRuleNotification(req notification.Request) (notification.Created, error) {
	nodes, err := client.GetNodes(h.nc, "all", req.RuleID, "decisionTable", false)
	if err != nil || len(nodes) != 1 || nodes[0].Parent != req.ScopeNodeID {
		return notification.Created{}, notification.ErrForbidden
	}
	if err := h.ValidateNotificationRecipients(req.ScopeNodeID, req.RecipientIDs); err != nil {
		return notification.Created{}, err
	}
	store, err := h.notificationStore()
	if err != nil {
		return notification.Created{}, err
	}
	return store.Create(req)
}

func notificationQuery(r *http.Request, keys ...string) (int, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if !slices.Contains(keys, key) || len(values) != 1 || len(values[0]) > 256 {
			return 0, false
		}
	}
	limit := 20
	if raw := query.Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return 0, false
		}
	}
	return limit, true
}
func (h *Handler) listNotifications(w http.ResponseWriter, r *http.Request, identity Identity) {
	limit, ok := notificationQuery(r, "limit", "before")
	if !ok {
		writeError(w, 400, "invalid_notification_query")
		return
	}
	store, err := h.notificationStore()
	if err != nil {
		writeError(w, 503, "notification_storage_unavailable")
		return
	}
	writeJSON(w, store.List(identity.UserID, r.URL.Query().Get("before"), limit, func(scope string) bool { return h.canReadNotification(identity, scope) }))
}
func (h *Handler) readNotification(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	if _, ok := notificationQuery(r); !ok {
		writeError(w, 400, "invalid_notification_query")
		return
	}
	// Explicit empty body: a client cannot name a different recipient or read time.
	var input struct{}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if !decodeJSON(w, r, &input) {
		return
	}
	store, err := h.notificationStore()
	if err != nil {
		writeError(w, 503, "notification_storage_unavailable")
		return
	}
	at, err := store.MarkRead(identity.UserID, id, func(scope string) bool { return h.canReadNotification(identity, scope) })
	if errors.Is(err, notification.ErrForbidden) {
		writeError(w, 403, "notification_forbidden")
		return
	}
	if err != nil {
		writeError(w, 503, "notification_read_not_saved")
		return
	}
	writeJSON(w, map[string]any{"id": id, "read_at": at})
}

type notificationRecipient struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

func (h *Handler) notificationRecipients(w http.ResponseWriter, r *http.Request, identity Identity) {
	limit, ok := notificationQuery(r, "limit", "after", "scope_node_id")
	scope := r.URL.Query().Get("scope_node_id")
	if !ok || scope == "" {
		writeError(w, 400, "invalid_notification_query")
		return
	}
	if !h.canReadNotification(identity, scope) {
		writeError(w, 403, "forbidden")
		return
	}
	nodes, err := client.GetNodes(h.nc, "all", "all", data.NodeTypeUser, false)
	if err != nil {
		writeError(w, 503, "notification_recipients_unavailable")
		return
	}
	slices.SortFunc(nodes, func(a, b data.NodeEdge) int { return strings.Compare(a.ID, b.ID) })
	out := []notificationRecipient{}
	seen := map[string]bool{}
	next := ""
	for _, node := range nodes {
		if seen[node.ID] || node.ID <= r.URL.Query().Get("after") {
			continue
		}
		seen[node.ID] = true
		subject, active := h.identityForUser(node.ID)
		if !active || !h.allows(subject, scope) {
			continue
		}
		if len(out) == limit {
			next = out[len(out)-1].ID
			break
		}
		first, _ := node.Points.Text(data.PointTypeFirstName, "")
		last, _ := node.Points.Text(data.PointTypeLastName, "")
		name := strings.TrimSpace(first + " " + last)
		if name == "" {
			name = "账号 " + node.ID
		}
		out = append(out, notificationRecipient{ID: node.ID, DisplayName: name})
	}
	writeJSON(w, struct {
		Items     []notificationRecipient `json:"items"`
		NextAfter string                  `json:"next_after,omitempty"`
	}{out, next})
}
