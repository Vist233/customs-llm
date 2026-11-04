package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Plan is a proposed change to a gateway. It waits here until a person decides in the web UI.
type Plan struct {
	ID      string          `json:"id"`
	Driver  string          `json:"driver"`
	URL     string          `json:"url"`
	FakeURL string          `json:"fake_url"`
	Actions []string        `json:"actions"`
	Routes  json.RawMessage `json:"routes"`
	Command []string        `json:"command,omitempty"`
	State   string          `json:"state"` // pending, approved, rejected, used
	Created time.Time       `json:"created"`
}

type plans struct {
	mu      sync.Mutex
	items   map[string]*Plan
	uiToken string
}

func (s *Server) uiToken() string {
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	if s.plans.uiToken == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		s.plans.uiToken = hex.EncodeToString(b)
	}
	return s.plans.uiToken
}

func (s *Server) addPlan(w http.ResponseWriter, r *http.Request) {
	var p Plan
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil || p.Driver == "" || len(p.Actions) == 0 {
		writeJSON(w, 400, map[string]string{"error": "need driver and actions"})
		return
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	p.ID, p.State, p.Created = hex.EncodeToString(b), "pending", time.Now()
	s.plans.mu.Lock()
	if s.plans.items == nil {
		s.plans.items = map[string]*Plan{}
	}
	s.plans.items[p.ID] = &p
	s.plans.mu.Unlock()
	writeJSON(w, 200, p)
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	out := []*Plan{}
	for _, p := range s.plans.items {
		if st := r.URL.Query().Get("state"); st == "" || st == p.State {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	writeJSON(w, 200, out)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	p := s.plans.items[r.PathValue("id")]
	if p == nil {
		writeJSON(w, 404, map[string]string{"error": "plan not found (plans do not survive a restart)"})
		return
	}
	writeJSON(w, 200, p)
}

// decidePlan accepts a decision only from the web page: the UI token lives in the page, not in daemon.json,
// and browsers set Sec-Fetch-Site themselves. A determined local process could still forge this, so it is
// a guard against an agent approving its own plan by accident, not a security boundary.
func (s *Server) decidePlan(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Customs-UI-Token") != s.uiToken() || r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		writeJSON(w, 403, map[string]string{"error": "a plan can only be decided in the web UI"})
		return
	}
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	p := s.plans.items[r.PathValue("id")]
	if p == nil || p.State != "pending" {
		writeJSON(w, 409, map[string]string{"error": "plan is not pending"})
		return
	}
	p.State = "rejected"
	if len(r.URL.Path) > 8 && r.URL.Path[len(r.URL.Path)-8:] == "/approve" {
		p.State = "approved"
	}
	writeJSON(w, 200, p)
}

// consumePlan hands an approved plan to one run; a plan cannot be used twice.
func (s *Server) consumePlan(w http.ResponseWriter, r *http.Request) {
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	p := s.plans.items[r.PathValue("id")]
	if p == nil {
		writeJSON(w, 404, map[string]string{"error": "plan not found (plans do not survive a restart)"})
		return
	}
	if p.State != "approved" {
		writeJSON(w, 409, p)
		return
	}
	p.State = "used"
	writeJSON(w, 200, p)
}
