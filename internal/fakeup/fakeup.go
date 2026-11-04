// Package fakeup is a fake upstream LLM API. It records every request it receives, byte for byte,
// and answers with either a scripted reply from a test case or a canned default per protocol.
package fakeup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Vist233/customs-llm/internal/proto"
	"github.com/Vist233/customs-llm/internal/testset"
)

const (
	Chat      = proto.Chat
	Responses = proto.Responses
	Anthropic = proto.Anthropic
	Gemini    = proto.Gemini
	Bedrock   = proto.Bedrock
	Ollama    = proto.Ollama
	JSONRPC   = proto.JSONRPC
)

// Record is one request the fake upstream received.
type Record struct {
	Method   string
	URI      string // path plus query, exactly as received
	Header   map[string]string
	Body     []byte
	At       time.Time
	Protocol string
	// What the fake answered, filled in once the reply has been written.
	Status      int
	RespType    string
	RespBody    []byte
	RespElapsed time.Duration
}

type Server struct {
	// Log, when set, receives one line per request.
	Log io.Writer
	// OnRecord, when set, is called after each recorded request has been answered.
	OnRecord func(Record)
	mu       sync.Mutex
	script   *testset.MockUpstream
	records  []Record
}

func New() *Server { return &Server{} }

// SetScript chooses the reply for subsequent requests; nil restores the canned defaults.
func (s *Server) SetScript(m *testset.MockUpstream) {
	s.mu.Lock()
	s.script = m
	s.mu.Unlock()
}

func (s *Server) Reset() {
	s.mu.Lock()
	s.records = nil
	s.mu.Unlock()
}

func (s *Server) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
}

// ProtocolOf maps a request path to the protocol it speaks, or "".
func ProtocolOf(path string) string { return proto.OfPath(path) }

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serve) }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	proto := ProtocolOf(r.URL.Path)
	rec := Record{Method: r.Method, URI: r.RequestURI, Header: map[string]string{}, Body: body, At: time.Now(), Protocol: proto}
	for k, v := range r.Header {
		rec.Header[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	if s.Log != nil {
		fmt.Fprintf(s.Log, "%s %s -> %s\n", r.Method, r.RequestURI, proto)
	}
	var idx = -1
	if proto != "" {
		s.mu.Lock()
		s.records = append(s.records, rec)
		idx = len(s.records) - 1
		s.mu.Unlock()
		cw := &capWriter{ResponseWriter: w, status: 200}
		w = cw
		defer func() {
			s.mu.Lock()
			s.records[idx].Status, s.records[idx].RespType = cw.status, cw.Header().Get("Content-Type")
			s.records[idx].RespBody, s.records[idx].RespElapsed = cw.buf.Bytes(), time.Since(rec.At)
			final := s.records[idx]
			s.mu.Unlock()
			if s.OnRecord != nil {
				s.OnRecord(final)
			}
		}()
	}
	if r.Method == http.MethodGet && strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/models") {
		writeJSON(w, 200, map[string]any{"object": "list", "data": []map[string]any{
			{"id": "customs-chat", "object": "model"}, {"id": "customs-responses", "object": "model"}, {"id": "customs-anthropic", "object": "model"}}})
		return
	}
	if proto == "" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	script := s.script
	s.mu.Unlock()
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	if m, _ := req["model"].(string); strings.HasPrefix(m, "fault-") && fault(w, r, m) {
		return
	}
	stream, _ := req["stream"].(bool)
	model, _ := req["model"].(string)
	lp := strings.ToLower(r.URL.Path)
	if strings.Contains(lp, ":streamgeneratecontent") || strings.HasSuffix(lp, "/converse-stream") {
		stream = true
	}
	if proto == Ollama {
		// Ollama streams unless told otherwise.
		if v, ok := req["stream"].(bool); !ok || v {
			stream = true
		} else {
			stream = false
		}
	}
	if script != nil {
		if stream && len(script.SSE) == 0 && script.Status < 400 {
			if st := streamed(proto, script); st != nil {
				script = st
			}
		}
		replyScript(w, proto, script)
		return
	}
	replyDefault(w, proto, stream, model, req)
}

// streamed renders a scripted whole reply as a stream, for gateways that always ask the upstream to stream.
func streamed(protocol string, m *testset.MockUpstream) *testset.MockUpstream {
	var o map[string]any
	if json.Unmarshal(m.Body, &o) != nil {
		return nil
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	var sse []string
	switch protocol {
	case Chat:
		ch, _ := o["choices"].([]any)
		if len(ch) == 0 {
			return nil
		}
		c, _ := ch[0].(map[string]any)
		chunk := func(delta any, fin any) string {
			return enc(map[string]any{"id": o["id"], "object": "chat.completion.chunk", "created": o["created"], "model": o["model"],
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": fin}}})
		}
		sse = append(sse, chunk(c["message"], nil), chunk(map[string]any{}, c["finish_reason"]))
		if u, ok := o["usage"]; ok {
			sse = append(sse, enc(map[string]any{"id": o["id"], "object": "chat.completion.chunk", "created": o["created"], "model": o["model"], "choices": []any{}, "usage": u}))
		}
		sse = append(sse, "[DONE]")
	case Responses:
		seq := 0
		ev := func(v map[string]any) {
			v["sequence_number"] = seq
			seq++
			sse = append(sse, enc(v))
		}
		start := map[string]any{}
		for k, v := range o {
			start[k] = v
		}
		start["status"], start["output"] = "in_progress", []any{}
		ev(map[string]any{"type": "response.created", "response": start})
		out, _ := o["output"].([]any)
		for i, it := range out {
			ev(map[string]any{"type": "response.output_item.done", "output_index": i, "item": it})
		}
		t := "response.completed"
		if o["status"] == "incomplete" {
			t = "response.incomplete"
		}
		ev(map[string]any{"type": t, "response": o})
	default:
		return nil
	}
	return &testset.MockUpstream{Status: m.Status, Header: m.Header, SSE: sse}
}

// StallLimit bounds how long the stall faults hang when the client never gives up.
var StallLimit = 10 * time.Minute

// fault reproduces upstream failures on demand, chosen by model name. It reports whether it handled the request.
func fault(w http.ResponseWriter, r *http.Request, model string) bool {
	chunk := `data: {"id":"f","object":"chat.completion.chunk","created":1,"model":"` + model + `","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}` + "\n\n"
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	wait := func() {
		select {
		case <-r.Context().Done():
		case <-time.After(StallLimit):
		}
	}
	startStream := func() {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, chunk)
		flush()
	}
	switch model {
	case "fault-empty400": // an error with no body: the client can only say "HTTP 400"
		w.WriteHeader(400)
	case "fault-empty500":
		w.WriteHeader(500)
	case "fault-quota-text": // a real reason in a body that is not JSON
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, "Weekly usage limit reached. Enable usage from your available balance.")
	case "fault-html502":
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(502)
		_, _ = io.WriteString(w, "<html><body><h1>502 Bad Gateway</h1></body></html>")
	case "fault-stall": // accepted, then no byte ever
		wait()
		panic(http.ErrAbortHandler)
	case "fault-stall-midstream": // one chunk, then silence
		startStream()
		wait()
		panic(http.ErrAbortHandler)
	case "fault-reset": // connection dropped before any byte
		panic(http.ErrAbortHandler)
	case "fault-reset-midstream": // connection dropped after one chunk
		startStream()
		panic(http.ErrAbortHandler)
	case "fault-no-terminal": // a stream that ends cleanly but never says it finished
		startStream()
	default:
		return false
	}
	return true
}

// capWriter copies what is written so the reply can be recorded.
type capWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (c *capWriter) WriteHeader(code int) { c.status = code; c.ResponseWriter.WriteHeader(code) }
func (c *capWriter) Write(b []byte) (int, error) {
	if c.buf.Len() < 4<<20 {
		c.buf.Write(b)
	}
	return c.ResponseWriter.Write(b)
}
func (c *capWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func replyScript(w http.ResponseWriter, proto string, m *testset.MockUpstream) {
	for k, v := range m.Header {
		w.Header().Set(k, v)
	}
	if len(m.SSE) == 0 {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(m.Status)
		_, _ = w.Write(m.Body)
		return
	}
	if m.Framing == "ndjson" {
		w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.WriteHeader(m.Status)
	fl, _ := w.(http.Flusher)
	for _, e := range m.SSE {
		writeEvent(w, proto, m.Framing, e)
		if fl != nil {
			fl.Flush()
		}
	}
}

func writeEvent(w io.Writer, proto, framing, entry string) {
	if framing == "ndjson" {
		_, _ = io.WriteString(w, entry+"\n")
		return
	}
	if strings.HasPrefix(entry, ":") {
		_, _ = io.WriteString(w, entry+"\n\n")
		return
	}
	if proto != Chat {
		var o struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(entry), &o) == nil && o.Type != "" {
			_, _ = io.WriteString(w, "event: "+o.Type+"\n")
		}
	}
	_, _ = io.WriteString(w, "data: "+entry+"\n\n")
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func replyDefault(w http.ResponseWriter, proto string, stream bool, model string, req map[string]any) {
	model = orDefault(model, "customs-fake")
	switch proto {
	case Chat:
		if !stream {
			writeJSON(w, 200, map[string]any{"id": "chatcmpl-fake", "object": "chat.completion", "created": 1, "model": model,
				"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4}})
			return
		}
		chunk := func(delta string, fin string) string {
			return `{"id":"chatcmpl-fake","object":"chat.completion.chunk","created":1,"model":"` + model + `","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + fin + `}]}`
		}
		replyScript(w, proto, &testset.MockUpstream{Status: 200, SSE: []string{
			chunk(`{"role":"assistant","content":"ok"}`, "null"), chunk("{}", `"stop"`),
			`{"id":"chatcmpl-fake","object":"chat.completion.chunk","created":1,"model":"` + model + `","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
			"[DONE]"}})
	case Responses:
		msg := map[string]any{"type": "message", "id": "msg_fake", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "ok", "annotations": []any{}}}}
		usage := map[string]any{"input_tokens": 3, "output_tokens": 1, "total_tokens": 4}
		resp := func(status string, out []any) map[string]any {
			return map[string]any{"id": "resp_fake", "object": "response", "created_at": 1, "status": status, "model": model, "output": out, "usage": usage}
		}
		if !stream {
			writeJSON(w, 200, resp("completed", []any{msg}))
			return
		}
		ev := func(v map[string]any) string { b, _ := json.Marshal(v); return string(b) }
		replyScript(w, proto, &testset.MockUpstream{Status: 200, SSE: []string{
			ev(map[string]any{"type": "response.created", "sequence_number": 0, "response": resp("in_progress", []any{})}),
			ev(map[string]any{"type": "response.output_text.delta", "sequence_number": 1, "output_index": 0, "item_id": "msg_fake", "content_index": 0, "delta": "ok"}),
			ev(map[string]any{"type": "response.output_item.done", "sequence_number": 2, "output_index": 0, "item": msg}),
			ev(map[string]any{"type": "response.completed", "sequence_number": 3, "response": resp("completed", []any{msg})})}})
	case Anthropic:
		if !stream {
			writeJSON(w, 200, map[string]any{"id": "msg_fake", "type": "message", "role": "assistant", "model": model,
				"content": []any{map[string]any{"type": "text", "text": "ok"}}, "stop_reason": "end_turn", "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": 3, "output_tokens": 1}})
			return
		}
		ev := func(v map[string]any) string { b, _ := json.Marshal(v); return string(b) }
		replyScript(w, proto, &testset.MockUpstream{Status: 200, SSE: []string{
			ev(map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_fake", "type": "message", "role": "assistant", "model": model,
				"content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 3, "output_tokens": 0}}}),
			ev(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}),
			ev(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "ok"}}),
			ev(map[string]any{"type": "content_block_stop", "index": 0}),
			ev(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 1}}),
			ev(map[string]any{"type": "message_stop"})}})
	case Gemini:
		cand := func(text string, fin any) map[string]any {
			c := map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}, "index": 0}
			if fin != nil {
				c["finishReason"] = fin
			}
			return c
		}
		usage := map[string]any{"promptTokenCount": 3, "candidatesTokenCount": 1, "totalTokenCount": 4}
		if !stream {
			writeJSON(w, 200, map[string]any{"candidates": []any{cand("ok", "STOP")}, "usageMetadata": usage, "modelVersion": model})
			return
		}
		b, _ := json.Marshal(map[string]any{"candidates": []any{cand("ok", "STOP")}, "usageMetadata": usage, "modelVersion": model})
		replyScript(w, proto, &testset.MockUpstream{Status: 200, SSE: []string{string(b)}})
	case Bedrock:
		writeJSON(w, 200, map[string]any{"output": map[string]any{"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"text": "ok"}}}},
			"stopReason": "end_turn", "usage": map[string]any{"inputTokens": 3, "outputTokens": 1, "totalTokens": 4}, "metrics": map[string]any{"latencyMs": 1}})
	case Ollama:
		msg := map[string]any{"role": "assistant", "content": "ok"}
		final := map[string]any{"model": model, "created_at": "2026-01-01T00:00:00Z", "message": msg, "done": true, "done_reason": "stop", "prompt_eval_count": 3, "eval_count": 1}
		if !stream {
			writeJSON(w, 200, final)
			return
		}
		b, _ := json.Marshal(final)
		replyScript(w, proto, &testset.MockUpstream{Status: 200, Framing: "ndjson", SSE: []string{string(b)}})
	case JSONRPC:
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"ok": true}})
	}
}
