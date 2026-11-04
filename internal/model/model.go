// Package model holds the records customs keeps: a Trace is one user request, made of Hops.
package model

import "time"

// Evidence tiers say how a hop was learned.
const (
	TierWire     = "wire"     // seen by a recorder sitting on the connection
	TierReported = "reported" // read from the gateway's own logs
	TierInferred = "inferred" // derived from other data
)

// Usage counts tokens as the upstream reported them. -1 means unknown.
type Usage struct {
	In         int `json:"in"`
	Out        int `json:"out"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
	Reasoning  int `json:"reasoning"`
}

func UnknownUsage() Usage { return Usage{-1, -1, -1, -1, -1} }

// Hop is one leg of a request: what one component received and what it answered.
type Hop struct {
	ID        int64             `json:"id"`
	TraceID   string            `json:"trace_id"`
	Seq       int               `json:"seq"`
	Source    string            `json:"source"` // relay, litellm, opencode, ...
	Tier      string            `json:"tier"`
	From      string            `json:"from"`
	To        string            `json:"to"`
	Started   time.Time         `json:"started"`
	FirstByte time.Time         `json:"first_byte,omitzero"`
	Ended     time.Time         `json:"ended,omitzero"`
	Protocol  string            `json:"protocol"`
	Method    string            `json:"method"`
	URI       string            `json:"uri"`
	ReqHeader map[string]string `json:"req_header,omitempty"`
	ReqBody   []byte            `json:"-"`
	Status    int               `json:"status"`
	RespHdr   map[string]string `json:"resp_header,omitempty"`
	RespBody  []byte            `json:"-"`
	Streaming bool              `json:"streaming"`
	Model     string            `json:"model,omitempty"`
	Finish    string            `json:"finish,omitempty"`
	Usage     Usage             `json:"usage"`
	Error     string            `json:"error,omitempty"` // transport or relay-level failure, not an HTTP error body
	Note      map[string]string `json:"note,omitempty"`
	Events    int               `json:"events"`
}

// Event is one piece of a streamed response.
type Event struct {
	HopID    int64  `json:"hop_id"`
	Idx      int    `json:"idx"`
	OffsetUS int64  `json:"offset_us"` // since the hop started
	Kind     string `json:"kind"`      // data, comment
	Name     string `json:"name,omitempty"`
	Data     []byte `json:"-"`
}

// TraceSummary is the one-line view of a trace used by lists.
type TraceSummary struct {
	ID         string    `json:"id"`
	Started    time.Time `json:"started"`
	Client     string    `json:"client"`
	Target     string    `json:"target"`
	Protocol   string    `json:"protocol"`
	Model      string    `json:"model"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	State      string    `json:"state"` // running, ok, http_error, transport_error, incomplete
	Streaming  bool      `json:"streaming"`
	Millis     int64     `json:"ms"`
	TTFBMillis int64     `json:"ttfb_ms"`
	Usage      Usage     `json:"usage"`
	Finish     string    `json:"finish"`
	Hops       int       `json:"hops"`
	Flags      []string  `json:"flags,omitempty"`
}
