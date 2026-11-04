// Package collect reads what gateways and clients already log and stores it as reported-tier hops.
// Reported hops are the component's own account, not something customs saw on the wire.
package collect

import (
	"context"
	"sync"
	"time"
)

// Status is shown on the sources page.
type Status struct {
	mu       sync.Mutex
	state    string
	detail   string
	imported int
}

func (s *Status) set(state, detail string) {
	s.mu.Lock()
	s.state, s.detail = state, detail
	s.mu.Unlock()
}

func (s *Status) add(n int) {
	s.mu.Lock()
	s.imported += n
	s.mu.Unlock()
}

// Get returns the state ("running" or "error"), a detail line and how many records were imported.
func (s *Status) Get() (string, string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.detail, s.imported
}

// Collector polls one source until its context ends.
type Collector interface {
	Name() string
	Poll(ctx context.Context) (int, error)
	State() *Status
}

// Run polls c every interval, reporting failures through its status rather than stopping.
func Run(ctx context.Context, c Collector, every time.Duration, idle string) {
	for {
		n, err := c.Poll(ctx)
		st := c.State()
		if err != nil {
			st.set("error", err.Error())
		} else {
			st.add(n)
			st.set("running", idle)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
