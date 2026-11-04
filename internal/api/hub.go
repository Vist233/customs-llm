package api

import "sync"

// Hub fans trace-change notifications out to live listeners (SSE clients).
type Hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan string]struct{}{}} }

func (h *Hub) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// Publish never blocks: a slow listener misses updates and refreshes on its next event.
func (h *Hub) Publish(traceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- traceID:
		default:
		}
	}
}
