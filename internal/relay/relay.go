// Package relay is the local forwarding proxy. It sits between a client and a gateway (or an upstream),
// forwards every request unchanged, and records both sides as a wire-tier hop.
package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Vist233/customs-llm/internal/ir"
	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/proto"
	"github.com/Vist233/customs-llm/internal/store"
)

// Target is a named upstream. Clients use http://relay/<name>/....
type Target struct {
	Name     string `json:"name"`
	Upstream string `json:"upstream"`
}

// Relay forwards and records.
type Relay struct {
	Store  *store.Store
	Client *http.Client
	// MaxBody caps how much of a body is kept (the forwarded bytes are never truncated).
	MaxBody int
	// OnChange is called after a trace changes, for live updates.
	OnChange func(traceID string)
	// Header added to forwarded requests so other recorders can link their hops to this trace.
	TraceHeader string

	mu      sync.RWMutex
	targets map[string]*url.URL
}

func New(st *store.Store) *Relay {
	return &Relay{Store: st, MaxBody: 32 << 20, TraceHeader: "X-Customs-Trace", targets: map[string]*url.URL{},
		Client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true,
			DisableCompression: true, MaxIdleConnsPerHost: 16, ResponseHeaderTimeout: 0}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// SetTarget adds or replaces a target.
func (r *Relay) SetTarget(name, upstream string) error {
	u, err := url.Parse(upstream)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("upstream must be an absolute URL: " + upstream)
	}
	r.mu.Lock()
	r.targets[name] = u
	r.mu.Unlock()
	return nil
}

func (r *Relay) Targets() []Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Target, 0, len(r.targets))
	for n, u := range r.targets {
		out = append(out, Target{Name: n, Upstream: u.String()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var hopByHop = map[string]bool{"connection": true, "proxy-connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true, "transfer-encoding": true, "upgrade": true}

func flat(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		m[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return m
}

func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/")
	name, rest, _ := strings.Cut(path, "/")
	r.mu.RLock()
	base := r.targets[name]
	r.mu.RUnlock()
	if base == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "unknown relay target " + name, "targets": r.Targets()})
		return
	}
	start := time.Now()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "reading request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/" + rest
	target.RawQuery = req.URL.RawQuery
	uri := "/" + rest
	if req.URL.RawQuery != "" {
		uri += "?" + req.URL.RawQuery
	}

	h := &model.Hop{TraceID: newID(), Source: "relay", Tier: model.TierWire, From: clientName(req), To: name, Started: start,
		Method: req.Method, URI: uri, Protocol: proto.OfPath(uri), ReqHeader: flat(req.Header), Usage: model.UnknownUsage()}
	h.ReqHeader["host"] = req.Host
	h.ReqBody = r.clip(body)
	var rq map[string]any
	if json.Unmarshal(body, &rq) == nil {
		h.Model, _ = rq["model"].(string)
		h.Streaming, _ = rq["stream"].(bool)
	}
	ctx := context.Background()
	_ = r.Store.SaveHop(ctx, h)
	r.changed(h.TraceID)

	out, err := http.NewRequestWithContext(req.Context(), req.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		r.fail(ctx, h, w, http.StatusBadGateway, "building upstream request: "+err.Error())
		return
	}
	for k, vs := range req.Header {
		if hopByHop[strings.ToLower(k)] || strings.EqualFold(k, "accept-encoding") || strings.EqualFold(k, "content-length") {
			continue
		}
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}
	out.Header.Set(r.TraceHeader, h.TraceID)
	out.ContentLength = int64(len(body))
	resp, err := r.Client.Do(out)
	if err != nil {
		if req.Context().Err() != nil {
			h.Error = "client closed the connection before the upstream answered"
			h.Ended = time.Now()
			_ = r.Store.SaveHop(ctx, h)
			r.changed(h.TraceID)
			return
		}
		r.fail(ctx, h, w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	h.Status = resp.StatusCode
	h.RespHdr = flat(resp.Header)
	stream := strings.Contains(resp.Header.Get("Content-Type"), "event-stream") || strings.Contains(resp.Header.Get("Content-Type"), "ndjson")
	h.Streaming = h.Streaming || stream
	for k, vs := range resp.Header {
		if hopByHop[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	fl, _ := w.(http.Flusher)

	var rec bytes.Buffer
	sse := &sseParser{start: start}
	buf := make([]byte, 32<<10)
	var readErr error
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if h.FirstByte.IsZero() {
				h.FirstByte = time.Now()
				_ = r.Store.SaveHop(ctx, h)
				r.changed(h.TraceID)
			}
			if rec.Len() < r.MaxBody {
				rec.Write(buf[:n])
			}
			if stream {
				sse.feed(buf[:n], time.Now())
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				readErr = errors.New("client went away while the response was streaming")
				break
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}
	h.Ended = time.Now()
	if readErr != nil {
		h.Error = "response interrupted: " + readErr.Error()
	}
	h.RespBody = rec.Bytes()
	evs := sse.events
	for i := range evs {
		evs[i].HopID = h.ID
	}
	if len(evs) > 0 {
		h.Events = len(evs)
		_ = r.Store.AddEvents(ctx, evs)
	}
	rr := ir.ReadResponse(h.Protocol, h.Status, h.RespBody, evs)
	h.Usage, h.Finish = rr.Usage, rr.Finish
	if rr.Model != "" {
		h.Model = rr.Model
	}
	_ = r.Store.SaveHop(ctx, h)
	r.changed(h.TraceID)
}

func (r *Relay) clip(b []byte) []byte {
	if len(b) > r.MaxBody {
		return b[:r.MaxBody]
	}
	return b
}

func (r *Relay) changed(id string) {
	if r.OnChange != nil {
		r.OnChange(id)
	}
}

func (r *Relay) fail(ctx context.Context, h *model.Hop, w http.ResponseWriter, status int, msg string) {
	h.Error, h.Ended = msg, time.Now()
	_ = r.Store.SaveHop(ctx, h)
	r.changed(h.TraceID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "type": "customs_relay_error"}})
}

func clientName(req *http.Request) string {
	if ua := req.Header.Get("User-Agent"); ua != "" {
		if len(ua) > 60 {
			ua = ua[:60]
		}
		return ua
	}
	return req.RemoteAddr
}

// sseParser splits a streamed body into events, stamping each with its arrival offset.
type sseParser struct {
	start  time.Time
	buf    []byte
	events []model.Event
}

func (p *sseParser) feed(b []byte, now time.Time) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.Index(p.buf, []byte("\n\n"))
		j := bytes.Index(p.buf, []byte("\r\n\r\n"))
		sep := 2
		if j >= 0 && (i < 0 || j < i) {
			i, sep = j, 4
		}
		if i < 0 {
			// ndjson: one JSON object per line
			if nl := bytes.IndexByte(p.buf, '\n'); nl >= 0 && !bytes.HasPrefix(p.buf, []byte("data:")) && !bytes.HasPrefix(p.buf, []byte("event:")) && !bytes.HasPrefix(p.buf, []byte(":")) {
				p.add(p.buf[:nl], now)
				p.buf = p.buf[nl+1:]
				continue
			}
			return
		}
		p.add(p.buf[:i], now)
		p.buf = p.buf[i+sep:]
	}
}

func (p *sseParser) add(block []byte, now time.Time) {
	block = bytes.TrimSpace(block)
	if len(block) == 0 {
		return
	}
	ev := model.Event{Idx: len(p.events), OffsetUS: now.Sub(p.start).Microseconds(), Kind: "data"}
	var data [][]byte
	for _, line := range bytes.Split(block, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		switch {
		case bytes.HasPrefix(line, []byte(":")):
			ev.Kind = "comment"
			data = append(data, line)
		case bytes.HasPrefix(line, []byte("event:")):
			ev.Name = string(bytes.TrimSpace(line[6:]))
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, bytes.TrimSpace(line[5:]))
		default:
			data = append(data, line) // ndjson or unknown line
		}
	}
	ev.Data = bytes.Join(data, []byte("\n"))
	p.events = append(p.events, ev)
}

// ParseStream splits an already complete SSE or NDJSON body into events (all stamped with offset 0).
func ParseStream(body []byte) []model.Event {
	p := &sseParser{}
	p.feed(append(append([]byte{}, body...), '\n', '\n'), time.Time{})
	return p.events
}
