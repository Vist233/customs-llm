// Package api serves the JSON/SSE interface used by the web UI and the command line.
package api

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Vist233/customs-llm/internal/ir"
	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/proto"
	"github.com/Vist233/customs-llm/internal/redact"
	"github.com/Vist233/customs-llm/internal/relay"
	"github.com/Vist233/customs-llm/internal/store"
)

//go:embed all:webdist
var webFS embed.FS

// Info is what /api/status reports about the running daemon.
type Info struct {
	Version     string   `json:"version"`
	DataDir     string   `json:"data_dir"`
	RelayListen string   `json:"relay_listen"`
	UIListen    string   `json:"ui_listen"`
	Sources     []Source `json:"sources"`
}

// Source describes one feed of data and how well it is working.
type Source struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`   // relay, collector
	Tier   string `json:"tier"`   // wire, reported
	Status string `json:"status"` // running, idle, error
	Detail string `json:"detail"`
}

// TestsetResults is supplied by the daemon so the api package does not depend on the test set.
type TestsetResults interface {
	Cases() any
	Results() any
}

type Server struct {
	Store   *store.Store
	Relay   *relay.Relay
	Hub     *Hub
	Token   string
	Info    func() Info
	Testset TestsetResults
	Guide   string
	// OnTargets is called after the relay targets change, so they can be persisted.
	OnTargets func()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/traces", s.listTraces)
	mux.HandleFunc("GET /api/traces/{id}", s.getTrace)
	mux.HandleFunc("GET /api/traces/{id}/reconcile", s.reconcile)
	mux.HandleFunc("GET /api/traces/{id}/hops/{hop}/body", s.body)
	mux.HandleFunc("GET /api/traces/{id}/hops/{hop}/events", s.events)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("GET /api/rows", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, ir.Rows) })
	mux.HandleFunc("GET /api/targets", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Relay.Targets()) })
	mux.HandleFunc("POST /api/targets", s.addTarget)
	mux.HandleFunc("GET /api/testset", s.testset)
	mux.HandleFunc("GET /api/guide", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = io.WriteString(w, s.Guide)
	})
	sub, _ := fs.Sub(webFS, "webdist")
	files := http.FileServer(http.FS(sub))
	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			b, err := fs.ReadFile(sub, "index.html")
			if err != nil {
				http.Error(w, "web assets missing", 500)
				return
			}
			// The page learns the token from its own HTML, so only a page served by this host can mutate state.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(strings.Replace(string(b), "__CUSTOMS_TOKEN__", s.Token, 1)))
			return
		}
		files.ServeHTTP(w, r)
	})
	return s.guard(mux)
}

// guard rejects requests whose Host is not a loopback name (DNS rebinding) and mutations without the token.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Customs-Token") != s.Token {
			http.Error(w, "missing or wrong X-Customs-Token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func wantRedact(r *http.Request) bool { return r.URL.Query().Get("redact") == "1" }

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	info := Info{}
	if s.Info != nil {
		info = s.Info()
	}
	all, _ := s.Store.ListTraces(r.Context(), store.Filter{Limit: 1000})
	failed := 0
	for _, t := range all {
		if t.State != "ok" && t.State != "running" {
			failed++
		}
	}
	writeJSON(w, 200, map[string]any{"info": info, "targets": s.Relay.Targets(), "traces": len(all), "failed": failed})
}

func (s *Server) listTraces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.Filter{State: q.Get("state"), Query: q.Get("q")}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	if d := q.Get("since"); d != "" {
		if dur, err := time.ParseDuration(d); err == nil {
			f.Since = time.Now().Add(-dur)
		}
	}
	list, err := s.Store.ListTraces(r.Context(), f)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []model.TraceSummary{}
	}
	writeJSON(w, 200, list)
}

type hopView struct {
	model.Hop
	ReqBytes  int `json:"req_bytes"`
	RespBytes int `json:"resp_bytes"`
}

func (s *Server) getTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.Store.Trace(r.Context(), id)
	if err != nil || t == nil {
		writeJSON(w, 404, map[string]string{"error": "trace not found"})
		return
	}
	hops, _ := s.Store.Hops(r.Context(), id)
	views := make([]hopView, len(hops))
	for i, h := range hops {
		v := hopView{Hop: h, ReqBytes: len(h.ReqBody), RespBytes: len(h.RespBody)}
		if wantRedact(r) {
			v.ReqHeader, v.RespHdr = redact.Headers(h.ReqHeader), redact.Headers(h.RespHdr)
			v.Error = redact.Text(h.Error)
		}
		views[i] = v
	}
	writeJSON(w, 200, map[string]any{"trace": t, "hops": views})
}

func (s *Server) hopByID(r *http.Request) (*model.Hop, bool) {
	id, _ := strconv.ParseInt(r.PathValue("hop"), 10, 64)
	hops, _ := s.Store.Hops(r.Context(), r.PathValue("id"))
	for i := range hops {
		if hops[i].ID == id {
			return &hops[i], true
		}
	}
	return nil, false
}

func (s *Server) body(w http.ResponseWriter, r *http.Request) {
	h, ok := s.hopByID(r)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "hop not found"})
		return
	}
	b := h.RespBody
	if r.URL.Query().Get("which") == "req" {
		b = h.ReqBody
	}
	if wantRedact(r) {
		b = redact.JSON(b)
	}
	ct := "application/octet-stream"
	if json.Valid(b) {
		ct = "application/json"
	} else if len(b) > 0 {
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(b)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	h, ok := s.hopByID(r)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "hop not found"})
		return
	}
	evs, _ := s.Store.Events(r.Context(), h.ID)
	type ev struct {
		Idx      int    `json:"idx"`
		OffsetUS int64  `json:"offset_us"`
		Kind     string `json:"kind"`
		Name     string `json:"name,omitempty"`
		Data     string `json:"data"`
	}
	out := make([]ev, len(evs))
	for i, e := range evs {
		d := string(e.Data)
		if wantRedact(r) {
			d = redact.Text(d)
		}
		out[i] = ev{e.Idx, e.OffsetUS, e.Kind, e.Name, d}
	}
	writeJSON(w, 200, out)
}

// reconcile builds the reconcile table: one column per hop that has a request or response to read.
func (s *Server) reconcile(w http.ResponseWriter, r *http.Request) {
	hops, err := s.Store.Hops(r.Context(), r.PathValue("id"))
	if err != nil || len(hops) == 0 {
		writeJSON(w, 404, map[string]string{"error": "trace not found"})
		return
	}
	var cols []ir.Column
	for _, h := range hops {
		facts := ir.Facts{}
		var top []string
		if h.Protocol != "" && len(h.ReqBody) > 0 {
			if f, keys, err := ir.Request(h.Protocol, h.ReqBody); err == nil {
				for k, v := range f {
					facts[k] = v
				}
				top = keys
			}
		}
		var evs []model.Event
		if h.Streaming {
			evs, _ = s.Store.Events(r.Context(), h.ID)
		}
		if !h.Ended.IsZero() {
			rr := ir.ReadResponse(h.Protocol, h.Status, h.RespBody, evs)
			for k, v := range rr.Facts {
				facts[k] = v
			}
			if rr.Finish != "" {
				facts["finish"] = rr.Finish
			}
			ir.UsageFacts(facts, rr.Usage.In, rr.Usage.Out, rr.Usage.CacheRead, rr.Usage.CacheWrite, rr.Usage.Reasoning)
		}
		name := h.Source
		if h.From != "" || h.To != "" {
			name = fmt.Sprintf("%s: %s → %s", h.Source, h.From, h.To)
		}
		cols = append(cols, ir.Column{Name: name, Tier: h.Tier, Protocol: h.Protocol, Facts: facts, TopKeys: top})
	}
	rows := ir.Reconcile(cols)
	if rows == nil {
		rows = []ir.Row{}
	}
	if wantRedact(r) {
		for i := range rows {
			for j := range rows[i].Cells {
				rows[i].Cells[j].Value = redact.Text(rows[i].Cells[j].Value)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"columns": cols, "rows": rows})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, cancel := s.Hub.Subscribe()
	defer cancel()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case id := <-ch:
			fmt.Fprintf(w, "event: trace\ndata: %s\n\n", id)
			fl.Flush()
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) addTarget(w http.ResponseWriter, r *http.Request) {
	var req relay.Target
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.Name == "" || strings.ContainsAny(req.Name, "/ ?#") {
		writeJSON(w, 400, map[string]string{"error": "need {name, upstream}; name must not contain / ? # or spaces"})
		return
	}
	if err := s.Relay.SetTarget(req.Name, req.Upstream); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if s.OnTargets != nil {
		s.OnTargets()
	}
	writeJSON(w, 200, s.Relay.Targets())
}

func (s *Server) testset(w http.ResponseWriter, r *http.Request) {
	if s.Testset == nil {
		writeJSON(w, 200, map[string]any{"cases": []any{}, "results": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"cases": s.Testset.Cases(), "results": s.Testset.Results()})
}

// Protocols exposes the protocol names to clients that want to label hops.
func Protocols() []string {
	p := []string{proto.Chat, proto.Responses, proto.Anthropic}
	sort.Strings(p)
	return p
}

var _ = context.Background
