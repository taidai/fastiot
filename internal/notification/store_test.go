package notification_test

import (
	"errors"
	"fastiot/internal/notification"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCursorMustBeCurrentlyVisibleToRecipient(t *testing.T) {
	s, err := notification.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	create := func(action, recipient, scope string) string {
		t.Helper()
		result, err := s.Create(notification.Request{SourceRevision: "v1", RuleID: "r", ExecutionID: "e", ActionID: action, ScopeNodeID: scope, RecipientIDs: []string{recipient}, Title: action})
		if err != nil {
			t.Fatal(err)
		}
		return result.ID
	}
	oldest := create("oldest", "alice", "site")
	older := create("older", "alice", "site")
	foreign := create("foreign", "bob", "site")
	revoked := create("revoked", "alice", "moved")
	newest := create("newest", "alice", "site")
	allow := func(scope string) bool { return scope == "site" }
	for _, cursor := range []string{foreign, revoked, "unknown"} {
		page := s.List("alice", cursor, 1, allow)
		if len(page.Items) != 0 || page.NextBefore != "" {
			t.Errorf("hidden/unknown cursor leaked pagination: %s %+v", cursor, page)
		}
	}
	first := s.List("alice", "", 1, allow)
	second := s.List("alice", first.NextBefore, 1, allow)
	third := s.List("alice", second.NextBefore, 1, allow)
	if len(first.Items) != 1 || first.Items[0].ID != newest || first.NextBefore != newest || len(second.Items) != 1 || second.Items[0].ID != older || second.NextBefore != older || len(third.Items) != 1 || third.Items[0].ID != oldest || third.NextBefore != "" {
		t.Fatalf("visible pagination changed: %+v %+v %+v", first, second, third)
	}
	restored := s.List("alice", revoked, 1, func(string) bool { return true })
	if len(restored.Items) != 1 || restored.Items[0].ID != older || restored.NextBefore != older {
		t.Fatalf("restored scope cursor: %+v", restored)
	}
}

func TestReadIsExplicitOwnScopedIdempotentAndDurable(t *testing.T) {
	dir := t.TempDir()
	s, err := notification.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	req := notification.Request{SourceRevision: "rev1", RuleID: "r", ExecutionID: "e", ActionID: "a", ScopeNodeID: "site", RecipientIDs: []string{"alice", "bob"}, Title: "Recorded"}
	created, err := s.Create(req)
	if err != nil {
		t.Fatal(err)
	}
	allow := func(string) bool { return true }
	deny := func(string) bool { return false }
	if _, err := s.MarkRead("outsider", created.ID, allow); !errors.Is(err, notification.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := s.MarkRead("alice", created.ID, deny); !errors.Is(err, notification.ErrForbidden) {
		t.Fatal(err)
	}
	at, err := s.MarkRead("alice", created.ID, allow)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.MarkRead("alice", created.ID, allow)
	if err != nil || !at.Equal(again) {
		t.Fatal("non-idempotent read", err)
	}
	s, err = notification.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List("alice", "", 20, allow).Items; len(got) != 1 || got[0].ReadAt == nil || !got[0].ReadAt.Equal(at) {
		t.Fatalf("lost read: %+v", got)
	}
	if s.List("bob", "", 20, allow).Items[0].ReadAt != nil {
		t.Fatal("read leaked to bob")
	}
	if len(s.List("alice", "", 20, deny).Items) != 0 {
		t.Fatal("scope revocation ignored")
	}
}

func TestConcurrentRetriesConflictAndPagination(t *testing.T) {
	s, err := notification.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req := notification.Request{SourceRevision: "rev1", RuleID: "r", ExecutionID: "e", ActionID: "a", ScopeNodeID: "site", RecipientIDs: []string{"alice", "bob"}, Title: "One"}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Create(req); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	allow := func(string) bool { return true }
	if len(s.List("alice", "", 100, allow).Items) != 1 {
		t.Fatal("duplicate record")
	}
	req.Body = "changed"
	if _, err := s.Create(req); !errors.Is(err, notification.ErrConflict) {
		t.Fatal("immutable payload accepted", err)
	}
	req.Body = ""
	req.RecipientIDs = []string{"bob", "alice", "alice"}
	if _, err := s.Create(req); err != nil {
		t.Fatal("recipient set order is not identity", err)
	}
	req.ActionID = "second"
	if _, err := s.Create(req); err != nil {
		t.Fatal(err)
	}
	first := s.List("alice", "", 1, allow)
	second := s.List("alice", first.NextBefore, 1, allow)
	if len(first.Items) != 1 || first.NextBefore == "" || len(second.Items) != 1 || first.Items[0].ID == second.Items[0].ID || second.NextBefore != "" {
		t.Fatalf("pagination: %+v %+v", first, second)
	}
}

func TestStorageFailureNeverAcknowledgesCreated(t *testing.T) {
	dir := t.TempDir()
	s, err := notification.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate unavailable storage at the public filesystem boundary.
	if err := os.Remove(filepath.Join(dir, "notifications.jsonl")); err != nil {
		t.Fatal(err)
	}
	req := notification.Request{SourceRevision: "v1", RuleID: "r", ExecutionID: "e", ActionID: "a", ScopeNodeID: "site", RecipientIDs: []string{"alice"}, Title: "Must persist"}
	result, err := s.Create(req)
	if err == nil || result.Status == "created" || len(s.List("alice", "", 20, func(string) bool { return true }).Items) != 0 {
		t.Fatal("false created on storage failure", result, err)
	}
}

func TestCreatedSurvivesRestartAndRetry(t *testing.T) {
	path := t.TempDir()
	store, err := notification.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	req := notification.Request{SourceRevision: "rev1", RuleID: "rule", ExecutionID: "execution", ActionID: "action", ScopeNodeID: "site", RecipientIDs: []string{"alice", "bob"}, Title: "Inspect pump", Body: "Pressure exceeded threshold"}
	created, err := store.Create(req)
	if err != nil || created.Status != "created" || created.ID == "" {
		t.Fatalf("create: %+v %v", created, err)
	}
	reopened, err := notification.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := reopened.Create(req)
	if err != nil || retry.ID != created.ID || !retry.Duplicate {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	page := reopened.List("alice", "", 20, func(string) bool { return true })
	if len(page.Items) != 1 || page.Items[0].Title != "Inspect pump" || page.Items[0].ReadAt != nil {
		t.Fatalf("inbox: %+v", page)
	}
}
