// Package notification stores in-app records, not transport deliveries or alarm evidence.
package notification

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid_notification")
	ErrConflict  = errors.New("notification_conflict")
	ErrForbidden = errors.New("notification_forbidden")
)

// Request is immutable. The producer must reuse execution/action identities on retry.
// Recipient IDs are local account references; imported projects must rebind them.
type Request struct {
	SourceRevision string   `json:"source_revision"`
	RuleID         string   `json:"rule_id"`
	ExecutionID    string   `json:"execution_id"`
	ActionID       string   `json:"action_id"`
	ScopeNodeID    string   `json:"scope_node_id"`
	RecipientIDs   []string `json:"recipient_ids"`
	Title          string   `json:"title"`
	Body           string   `json:"body"`
}
type Created struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // created never means delivered or read
	CreatedAt time.Time `json:"created_at"`
	Duplicate bool      `json:"duplicate"`
}
type Item struct {
	ID          string     `json:"id"`
	RuleID      string     `json:"rule_id"`
	ScopeNodeID string     `json:"scope_node_id"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	CreatedAt   time.Time  `json:"created_at"`
	ReadAt      *time.Time `json:"read_at"`
}
type Page struct {
	Items      []Item `json:"items"`
	NextBefore string `json:"next_before,omitempty"`
}
type record struct {
	Request Request              `json:"request"`
	Created Created              `json:"created"`
	Reads   map[string]time.Time `json:"-"`
}
type event struct {
	Record *record   `json:"record,omitempty"`
	ID     string    `json:"id,omitempty"`
	UserID string    `json:"user_id,omitempty"`
	ReadAt time.Time `json:"read_at,omitempty"`
}

// Store is single-process (the app owns the data-directory lock).
// ponytail: append-only journal and an in-memory index; no retention/compaction yet.
type Store struct {
	mu      sync.Mutex
	path    string
	records map[string]*record
	order   []string
	failed  error
}

func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("notification_storage_unavailable")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "notifications.jsonl"), records: map[string]*record{}}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var offset int64
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			// An interrupted final append was never acknowledged; discard only its tail.
			if len(line) > 0 {
				if err := f.Truncate(offset); err != nil {
					return nil, err
				}
			}
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("notification journal: %w", err)
		}
		if e.Record != nil {
			r := e.Record
			r.Reads = map[string]time.Time{}
			if r.Created.ID == "" || s.records[r.Created.ID] != nil {
				return nil, ErrConflict
			}
			s.records[r.Created.ID] = r
			s.order = append(s.order, r.Created.ID)
		} else {
			r := s.records[e.ID]
			if r == nil || !slices.Contains(r.Request.RecipientIDs, e.UserID) || e.ReadAt.IsZero() {
				return nil, ErrInvalid
			}
			r.Reads[e.UserID] = e.ReadAt
		}
		offset += int64(len(line))
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	// Persist directory entry before any producer can acknowledge a created record.
	d, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return nil, err
	}
	return s, nil
}
func normalize(req Request) (Request, error) {
	for _, value := range []string{req.RuleID, req.SourceRevision, req.ExecutionID, req.ActionID, req.ScopeNodeID, req.Title} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return Request{}, ErrInvalid
		}
	}
	if len(req.Body) > 8192 || len(req.RecipientIDs) == 0 || len(req.RecipientIDs) > 100 {
		return Request{}, ErrInvalid
	}
	req.RecipientIDs = slices.Clone(req.RecipientIDs)
	slices.Sort(req.RecipientIDs)
	req.RecipientIDs = slices.Compact(req.RecipientIDs)
	for _, id := range req.RecipientIDs {
		if strings.TrimSpace(id) == "" || len(id) > 256 {
			return Request{}, ErrInvalid
		}
	}
	return req, nil
}
func (s *Store) append(e event) error {
	if s.failed != nil {
		return s.failed
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	// Ambiguous writes fail closed until reopen, rather than append behind a torn event.
	if err != nil {
		s.failed = err
	}
	return err
}
func (s *Store) Create(req Request) (Created, error) {
	req, err := normalize(req)
	if err != nil {
		return Created{}, err
	}
	key, _ := json.Marshal([]string{req.RuleID, req.ExecutionID, req.ActionID})
	hash := sha256.Sum256(key)
	id := hex.EncodeToString(hash[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return Created{}, s.failed
	}
	if old := s.records[id]; old != nil {
		a, _ := json.Marshal(old.Request)
		b, _ := json.Marshal(req)
		if string(a) != string(b) {
			return Created{}, ErrConflict
		}
		result := old.Created
		result.Duplicate = true
		return result, nil
	}
	r := &record{Request: req, Created: Created{ID: id, Status: "created", CreatedAt: time.Now().UTC()}, Reads: map[string]time.Time{}}
	if err := s.append(event{Record: r}); err != nil {
		return Created{}, err
	}
	s.records[id] = r
	s.order = append(s.order, id)
	return r.Created, nil
}

// MarkRead changes only this recipient's acknowledgement, after current authority.
func (s *Store) MarkRead(userID, id string, allow func(string) bool) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[id]
	if r == nil || !slices.Contains(r.Request.RecipientIDs, userID) || !allow(r.Request.ScopeNodeID) {
		return time.Time{}, ErrForbidden
	}
	if at, ok := r.Reads[userID]; ok {
		return at, nil
	}
	at := time.Now().UTC()
	if err := s.append(event{ID: id, UserID: userID, ReadAt: at}); err != nil {
		return time.Time{}, err
	}
	r.Reads[userID] = at
	return at, nil
}
func (s *Store) List(userID, before string, limit int, allow func(string) bool) Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > 100 {
		limit = 20
	}
	page := Page{Items: []Item{}}
	start := len(s.order) - 1
	if before != "" {
		// A cursor is a record reference, so authorize it before using its
		// journal position. Hidden and unknown IDs must produce the same page.
		cursor := s.records[before]
		if cursor == nil || !slices.Contains(cursor.Request.RecipientIDs, userID) || !allow(cursor.Request.ScopeNodeID) {
			return page
		}
		start = slices.Index(s.order, before) - 1
	}
	for i := start; i >= 0; i-- {
		r := s.records[s.order[i]]
		if !slices.Contains(r.Request.RecipientIDs, userID) || !allow(r.Request.ScopeNodeID) {
			continue
		}
		if len(page.Items) == limit {
			page.NextBefore = page.Items[len(page.Items)-1].ID
			break
		}
		item := Item{ID: r.Created.ID, RuleID: r.Request.RuleID, ScopeNodeID: r.Request.ScopeNodeID, Title: r.Request.Title, Body: r.Request.Body, CreatedAt: r.Created.CreatedAt}
		if at, ok := r.Reads[userID]; ok {
			item.ReadAt = &at
		}
		page.Items = append(page.Items, item)
	}
	return page
}
