// Package probe runs test set cases against a live gateway whose upstream is the fake upstream.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Vist233/customs-llm/internal/checks"
	"github.com/Vist233/customs-llm/internal/fakeup"
	"github.com/Vist233/customs-llm/internal/testset"
)

type Outcome string

const (
	Pass         Outcome = "PASS"         // the assertions hold: the gateway kept what it should
	Fail         Outcome = "FAIL"         // an assertion is violated: the reported behaviour is present
	Inconclusive Outcome = "INCONCLUSIVE" // the run could not test the case (route, rejection, no upstream hit)
	Skip         Outcome = "SKIP"         // the case cannot be run with this setup
)

// Target describes a gateway under test.
type Target struct {
	Name    string
	BaseURL string
	Key     string
	// Aliases maps "<client protocol>-><upstream protocol>" to the gateway model that routes to the
	// fake upstream that way. "*-><upstream protocol>" is the fallback for any client protocol.
	// Example: {"*->openai_chat": "customs-chat", "openai_responses->openai_chat": "customs-chat-bridge"}.
	Aliases map[string]string
	// WithPreconditions also runs cases that declare preconditions (they usually will not trigger).
	WithPreconditions bool
	// Satisfied lists case ids whose preconditions the gateway setup already meets.
	Satisfied map[string]bool
}

type CaseResult struct {
	ID           string  `json:"id"`
	Gateway      string  `json:"gateway"`
	Outcome      Outcome `json:"outcome"`
	Detail       string  `json:"detail"`
	ClientStatus int     `json:"client_status,omitempty"`
	UpstreamPath string  `json:"upstream_path,omitempty"`
	Millis       int64   `json:"ms"`
	// Observed is filled when Runner.Dump is set: what the run captured, for checking the checker.
	Observed *checks.Observation `json:"observed,omitempty"`
}

// Run is everything captured for one executed case, for callers that want to keep it.
type Run struct {
	Case         *testset.Case
	Result       CaseResult
	Sent         []byte
	SentAt       time.Time
	ClientStatus int
	ClientType   string
	ClientBody   []byte
	Upstream     fakeup.Record
	Elapsed      time.Duration
}

type Runner struct {
	Target Target
	Fake   *fakeup.Server
	HTTP   *http.Client
	Dump   bool
	// Sink, when set, receives every case that reached the fake upstream.
	Sink func(Run)
}

func NewRunner(t Target, f *fakeup.Server) *Runner {
	return &Runner{Target: t, Fake: f, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (r *Runner) RunAll(ctx context.Context, cases []testset.Case, only map[string]bool) []CaseResult {
	var out []CaseResult
	for i := range cases {
		if len(only) > 0 && !only[cases[i].ID] {
			continue
		}
		out = append(out, r.Run(ctx, &cases[i]))
	}
	return out
}

func (r *Runner) Run(ctx context.Context, c *testset.Case) CaseResult {
	start := time.Now()
	res := CaseResult{ID: c.ID, Gateway: r.Target.Name}
	done := func(o Outcome, format string, a ...any) CaseResult {
		res.Outcome, res.Detail, res.Millis = o, fmt.Sprintf(format, a...), time.Since(start).Milliseconds()
		return res
	}
	clientProto := fakeup.ProtocolOf(c.Inbound.Path)
	alias, specific := r.pickAlias(c, clientProto)
	if clientProto == "" && r.Target.Aliases["path:"+pathOnly(c.Inbound.Path)] != "" {
		alias = "pass-through"
	}
	switch {
	case clientProto == "" && r.Target.Aliases["path:"+pathOnly(c.Inbound.Path)] == "":
		return done(Skip, "inbound path %s is not a chat, responses or messages endpoint and is not configured as a pass-through", c.Inbound.Path)
	case alias == "":
		return done(Skip, "no fake model for %s -> %s", clientProto, c.Protocol.Out)
	case c.Upstream != nil && c.Upstream.Framing == "aws_eventstream":
		return done(Skip, "fake upstream cannot speak aws_eventstream yet")
	case len(c.Preconditions) > 0 && !r.Target.WithPreconditions && !r.Target.Satisfied[c.ID]:
		return done(Skip, "needs precondition: %s", strings.Join(c.Preconditions, "; "))
	}
	var inbound map[string]any
	if err := json.Unmarshal(c.Inbound.Body, &inbound); err != nil {
		return done(Inconclusive, "inbound body is not a JSON object: %v", err)
	}
	var source any = inbound
	if c.Direction != "request" && c.Upstream != nil && len(c.Upstream.Body) > 0 {
		_ = json.Unmarshal(c.Upstream.Body, &source)
	}
	sent := make(map[string]any, len(inbound))
	for k, v := range inbound {
		sent[k] = v
	}
	if clientProto != "" {
		sent["model"] = alias
	}
	payload, _ := json.Marshal(sent)

	r.Fake.Reset()
	r.Fake.SetScript(c.Upstream)
	defer r.Fake.SetScript(nil)

	req, err := http.NewRequestWithContext(ctx, c.Inbound.Method, strings.TrimRight(r.Target.BaseURL, "/")+c.Inbound.Path, bytes.NewReader(payload))
	if err != nil {
		return done(Inconclusive, "building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Target.Key != "" {
		req.Header.Set("Authorization", "Bearer "+r.Target.Key)
	}
	if fakeup.ProtocolOf(c.Inbound.Path) == fakeup.Anthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	for k, v := range c.Inbound.Headers {
		if strings.EqualFold(k, "authorization") && r.Target.Key != "" {
			continue // the case's placeholder credential must not replace the gateway key
		}
		req.Header.Set(k, v)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return done(Inconclusive, "request to gateway failed: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	res.ClientStatus = resp.StatusCode

	recs := r.Fake.Records()
	if len(recs) == 0 {
		return done(Inconclusive, "gateway answered %d without reaching the fake upstream: %s", resp.StatusCode, snippet(raw))
	}
	last := recs[len(recs)-1]
	res.UpstreamPath = last.URI
	if last.Protocol != c.Protocol.Out && !(clientProto == "" && last.Protocol == fakeup.JSONRPC) {
		return done(Inconclusive, "gateway called the %s route, the case needs %s", last.Protocol, c.Protocol.Out)
	}
	obs := &checks.Observation{Source: source, UpstreamHeaders: last.Header, UpstreamURL: last.URI}
	var up any
	if err := json.Unmarshal(last.Body, &up); err != nil {
		up = map[string]any{"_raw": string(last.Body)}
	}
	obs.UpstreamRequest = up
	obs.ClientResponse = ParseClientResponse(resp.Header.Get("Content-Type"), raw)
	if c.Direction == "request" && resp.StatusCode >= 400 && len(recs) == 0 {
		return done(Inconclusive, "gateway rejected the request: %d", resp.StatusCode)
	}
	if r.Dump {
		res.Observed = obs
	}
	// A gateway that failed on the mock did not get as far as what the case looks at, so nothing can be concluded.
	if (c.Direction == "response" || c.Direction == "stream") && resp.StatusCode >= 400 {
		return done(Inconclusive, "gateway answered %d to the mock reply: %s", resp.StatusCode, snippet(raw))
	}
	v := checks.EvaluateCase(c, obs)
	if r.Sink != nil {
		defer func() {
			r.Sink(Run{Case: c, Result: res, Sent: payload, SentAt: start, ClientStatus: resp.StatusCode, ClientType: resp.Header.Get("Content-Type"),
				ClientBody: raw, Upstream: last, Elapsed: time.Since(start)})
		}()
	}
	if !v.Pass {
		return done(Fail, "%s", v.Detail)
	}
	if specific != "" {
		return done(Inconclusive, "passes on a generic provider, but the case is specific to %q, which this setup does not route to: %s", specific, v.Detail)
	}
	return done(Pass, "%s", v.Detail)
}

// providerWords name providers whose own request handling a case may depend on.
var providerWords = []string{"mistral", "codestral", "minimax", "bedrock", "gemini", "google", "vertex", "databricks",
	"oci", "ollama", "azure", "deepseek", "chatgpt", "codex", "venus"}

// pickAlias chooses the gateway model for a case. Aliases keyed "route:<word>" win when the case's route
// mentions <word>; their value may end in "@<upstream protocol>" to apply only to cases with that protocol. The second result is the provider word the case is specific to but no alias covers.
func (r *Runner) pickAlias(c *testset.Case, clientProto string) (alias, uncovered string) {
	route := strings.ToLower(c.Gateway.Route)
	for _, w := range providerWords {
		if !strings.Contains(route, w) {
			continue
		}
		if a := r.Target.Aliases["route:"+w]; a != "" {
			model, proto, _ := strings.Cut(a, "@")
			if proto == "" || proto == c.Protocol.Out {
				return model, ""
			}
		}
		if uncovered == "" {
			uncovered = w
		}
	}
	alias = r.Target.Aliases[clientProto+"->"+c.Protocol.Out]
	if alias == "" {
		alias = r.Target.Aliases["*->"+c.Protocol.Out]
	}
	return alias, uncovered
}

func pathOnly(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// ParseClientResponse turns a response body into the shape assertions see: the parsed JSON, or for
// event streams {"events":[...],"comments":[...]}.
func ParseClientResponse(contentType string, raw []byte) any {
	if strings.Contains(contentType, "event-stream") {
		events, comments := []any{}, []any{}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimRight(line, "\r")
			switch {
			case strings.HasPrefix(line, ":"):
				comments = append(comments, line)
			case strings.HasPrefix(line, "data:"):
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				var v any
				if json.Unmarshal([]byte(data), &v) == nil {
					events = append(events, v)
				} else {
					events = append(events, data)
				}
			}
		}
		return map[string]any{"events": events, "comments": comments}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{"_raw": string(raw)}
	}
	return v
}
