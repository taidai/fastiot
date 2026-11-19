package genscreen

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

// Session is one running preview. Its capability authorises the sandboxed
// frame to be served; its owner authorises every API call.
type Session struct {
	mu         sync.Mutex
	ID         string
	Capability string
	Owner      string
	NodeID     string
	Source     string
	Points     []Point
	Sim        *Simulator
	Static     []Finding
	Client     []Finding
	Revision   int
	Created    time.Time
	Checked    time.Time
}

// Snapshot is the preview's source and model at one instant. Revision can
// replace the source while a request is in flight, so callers read it through
// here instead of touching the fields.
type Snapshot struct {
	Source   string
	Points   []Point
	Revision int
}

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{Source: s.Source, Points: append([]Point{}, s.Points...), Revision: s.Revision}
}

// ApplyRevision replaces the screen with the model's modified document. The
// previous browser checks no longer describe it, so they are cleared and the
// publish gate must be satisfied again.
func (s *Session) ApplyRevision(source string, static []Finding) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Source = source
	s.Static = static
	s.Client = nil
	s.Checked = time.Time{}
	s.Revision++
	return s.Revision
}

// HasPoint reports whether the point belongs to the model this preview was
// generated from. It is the only target set a page may operate.
func (s *Session) HasPoint(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, point := range s.Points {
		if point.ID == id {
			return true
		}
	}
	return false
}

// Report merges the server checks with the latest browser checks.
func (s *Session) Report() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reportLocked()
}

func (s *Session) reportLocked() Report { return MergeReports(s.Static, s.Client) }

// SetClient replaces the browser checks and records when they ran.
func (s *Session) SetClient(findings []Finding, at time.Time) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Client = findings
	s.Checked = at
	return s.reportLocked()
}

// CheckedAt is when the browser checks last ran for this session.
func (s *Session) CheckedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Checked
}

// HasClientChecks reports whether the browser checks ran for this session.
func (s *Session) HasClientChecks() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.Checked.IsZero()
}

// Store keeps previews in memory. Previews are short-lived working state,
// not project data; anything worth keeping is confirmed onto a node.
type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
	order    []string
	max      int
	ttl      time.Duration
	Now      func() time.Time
}

func NewStore(max int, ttl time.Duration) *Store {
	if max <= 0 {
		max = 32
	}
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return &Store{sessions: map[string]*Session{}, max: max, ttl: ttl, Now: time.Now}
}

// Put assigns identity and stores the session, evicting the oldest when the
// store is full.
func (s *Store) Put(session *Session) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	s.pruneLocked(now)
	if session.ID == "" {
		session.ID = randomHex(16)
	}
	if session.Capability == "" {
		session.Capability = randomHex(32)
	}
	session.Created = now
	if session.Sim == nil {
		session.Sim = NewSimulator(session.Points)
	}
	for len(s.sessions) >= s.max && len(s.order) > 0 {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.sessions, oldest)
	}
	s.sessions[session.ID] = session
	s.order = append(s.order, session.ID)
	return session
}

// Get returns a session only to its owner.
func (s *Store) Get(id, owner string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.Now())
	session, ok := s.sessions[id]
	if !ok || session.Owner != owner {
		return nil, false
	}
	return session, true
}

// ByCapability resolves the frame request. The capability never names a user
// and grants nothing beyond the frame's own simulated data.
func (s *Store) ByCapability(capability string) (*Session, bool) {
	if capability == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.Now())
	for _, session := range s.sessions {
		if subtle.ConstantTimeCompare([]byte(session.Capability), []byte(capability)) == 1 {
			return session, true
		}
	}
	return nil, false
}

func (s *Store) pruneLocked(now time.Time) {
	kept := s.order[:0]
	for _, id := range s.order {
		session, ok := s.sessions[id]
		if !ok {
			continue
		}
		if now.Sub(session.Created) > s.ttl {
			delete(s.sessions, id)
			continue
		}
		kept = append(kept, id)
	}
	s.order = kept
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		panic("genscreen: no randomness for preview identity")
	}
	return hex.EncodeToString(buffer)
}
