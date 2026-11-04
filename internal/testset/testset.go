// Package testset defines and loads the fidelity cases mined from real gateway issues.
//
// A case is one canonical request (or mock upstream reply) plus assertions about what
// must survive the gateway. The same cases drive both the checker and, later, regression runs.
package testset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

type Source struct {
	Repo  string `json:"repo"`
	Issue int    `json:"issue"`
	URL   string `json:"url"`
	State string `json:"state"` // open | closed, as of collection
}

type Gateway struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// Route is the upstream provider/channel the issue was observed on, e.g. "bedrock/converse".
	Route string `json:"route,omitempty"`
}

type Protocol struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body"`
}

// MockUpstream is what the recording upstream replies with, for response/stream/error cases.
type MockUpstream struct {
	Status int               `json:"status"`
	Header map[string]string `json:"headers,omitempty"`
	Body   json.RawMessage   `json:"body,omitempty"`
	// SSE holds raw event payloads, one entry per `data:` line, in order.
	SSE []string `json:"sse,omitempty"`
	// Framing is how SSE entries must be encoded on the wire: sse (default), ndjson or aws_eventstream.
	Framing string `json:"framing,omitempty"`
}

// Assertion is one invariant. Paths are a JSONPath subset: $.a.b[0].c, with [*] meaning "any element".
//
// Targets: upstream_request is the JSON body the gateway sends upstream (the final one if it
// retries or falls back); upstream_headers is a flat object of lowercase header names;
// upstream_url is the path plus query as a JSON string. client_response is the JSON body the
// client receives; for streams it is {"events":[<each data payload parsed as JSON>]}.
type Assertion struct {
	Op string `json:"op"`
	// Target says which message the paths refer to. Empty means upstream_request for
	// request cases and client_response for response/stream/error cases.
	Target string `json:"target,omitempty"`
	In     string `json:"in,omitempty"`
	Out    string `json:"out,omitempty"`
	Value  any    `json:"value,omitempty"`
	// Text is matched against the compact JSON of the whole target message.
	Text  string      `json:"text,omitempty"`
	Items []Assertion `json:"items,omitempty"`
	Why   string      `json:"why,omitempty"`
}

type Case struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Source    Source   `json:"source"`
	Gateway   Gateway  `json:"gateway"`
	Category  string   `json:"category"`
	Direction string   `json:"direction"`
	Protocol  Protocol `json:"protocol"`
	Severity  string   `json:"severity"`
	// Detectable says whether the loss is visible in bytes crossing the gateway boundary.
	Detectable string `json:"detectable"`
	// PayloadOrigin: issue = payload copied from the issue; reconstructed = minimal payload we wrote.
	PayloadOrigin string `json:"payload_origin"`
	// Verified is true only when we have run the case against a real gateway.
	Verified      bool          `json:"verified"`
	Preconditions []string      `json:"preconditions,omitempty"`
	Inbound       Request       `json:"inbound"`
	Upstream      *MockUpstream `json:"upstream,omitempty"`
	Assertions    []Assertion   `json:"assertions"`
	Observed      string        `json:"observed"`
	Notes         string        `json:"notes,omitempty"`
	// Examples are optional recorded messages that make the case self-testing: the assertions
	// must fail on Bad and pass on Good, so the checker is verified without a real gateway.
	Examples *Examples `json:"examples,omitempty"`
}

// Examples holds a message as the target (see Assertion.Target) looked when buggy and when correct.
type Examples struct {
	Bad  json.RawMessage `json:"bad,omitempty"`
	Good json.RawMessage `json:"good,omitempty"`
}

var (
	directions = set("request", "response", "stream", "error")
	protocols  = set("openai_chat", "openai_responses", "anthropic_messages", "gemini_generate",
		"bedrock_converse", "jsonrpc", "other")
	severities = set("critical", "high", "medium", "low")
	detect     = set("yes", "partial", "no")
	origins    = set("issue", "reconstructed")
	targets    = set("", "upstream_request", "upstream_headers", "upstream_url", "client_response")
	framings   = set("", "sse", "ndjson", "aws_eventstream")
	categories = set(
		"field_dropped", "field_injected", "field_mutated", "tool_choice_lost", "tool_schema_rewritten",
		"content_block_lost", "message_lost", "semantic_lost", "cache_control_lost", "reasoning_lost",
		"untranslatable_passed_through", "format_shape_changed", "stream_chunk_lost", "usage_lost",
		"error_masked", "finish_reason_wrong", "header_conflict", "state_mutation",
	)
	ops = set("present", "absent", "equals", "preserved", "type_is",
		"contains_anywhere", "absent_anywhere", "any_of", "all_of")
	jsonTypes = set("string", "array", "object", "number", "boolean", "null")
	pathRe    = regexp.MustCompile(`^\$(\.[A-Za-z_][A-Za-z0-9_-]*|\[(\d+|\*)\])*$`)
	idRe      = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// Load reads every *.json file in dir, rejecting unknown fields and invalid cases.
func Load(dir string) ([]Case, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []Case
	seen := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var c Case
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if g := filepath.Base(filepath.Dir(f)); g != c.Gateway.Name {
			return nil, fmt.Errorf("%s: must live in a directory named after its gateway (%s)", f, c.Gateway.Name)
		}
		if want := c.ID + ".json"; filepath.Base(f) != want {
			return nil, fmt.Errorf("%s: file name must be %s", f, want)
		}
		if prev, ok := seen[c.ID]; ok {
			return nil, fmt.Errorf("%s: duplicate id, also in %s", f, prev)
		}
		seen[c.ID] = f
		out = append(out, c)
	}
	return out, nil
}

func (c *Case) Validate() error {
	switch {
	case !idRe.MatchString(c.ID):
		return fmt.Errorf("bad id %q", c.ID)
	case c.Title == "":
		return fmt.Errorf("title required")
	case c.Source.Repo == "" || c.Source.Issue <= 0 || c.Source.URL == "":
		return fmt.Errorf("source.repo, source.issue and source.url required")
	case c.Gateway.Name == "":
		return fmt.Errorf("gateway.name required")
	case !categories[c.Category]:
		return fmt.Errorf("unknown category %q", c.Category)
	case !directions[c.Direction]:
		return fmt.Errorf("unknown direction %q", c.Direction)
	case !protocols[c.Protocol.In] || !protocols[c.Protocol.Out]:
		return fmt.Errorf("unknown protocol %q -> %q", c.Protocol.In, c.Protocol.Out)
	case !severities[c.Severity]:
		return fmt.Errorf("unknown severity %q", c.Severity)
	case !detect[c.Detectable]:
		return fmt.Errorf("unknown detectable %q", c.Detectable)
	case !origins[c.PayloadOrigin]:
		return fmt.Errorf("unknown payload_origin %q", c.PayloadOrigin)
	case c.Observed == "":
		return fmt.Errorf("observed required")
	case len(c.Assertions) == 0:
		return fmt.Errorf("at least one assertion required")
	case c.Inbound.Method == "" || c.Inbound.Path == "" || !json.Valid(c.Inbound.Body):
		return fmt.Errorf("inbound needs method, path and a valid JSON body")
	}
	if c.Direction != "request" && c.Upstream == nil {
		return fmt.Errorf("%s case needs an upstream mock", c.Direction)
	}
	if c.Upstream != nil {
		if c.Upstream.Status == 0 {
			return fmt.Errorf("upstream.status required")
		}
		if !framings[c.Upstream.Framing] {
			return fmt.Errorf("unknown upstream.framing %q", c.Upstream.Framing)
		}
		if len(c.Upstream.Body) > 0 && !json.Valid(c.Upstream.Body) {
			return fmt.Errorf("upstream.body is not valid JSON")
		}
	}
	if e := c.Examples; e != nil {
		if len(e.Bad) == 0 && len(e.Good) == 0 {
			return fmt.Errorf("examples needs bad or good")
		}
		for name, raw := range map[string]json.RawMessage{"bad": e.Bad, "good": e.Good} {
			if len(raw) > 0 && !json.Valid(raw) {
				return fmt.Errorf("examples.%s is not valid JSON", name)
			}
		}
	}
	for i := range c.Assertions {
		if err := c.Assertions[i].validate(); err != nil {
			return fmt.Errorf("assertions[%d]: %w", i, err)
		}
	}
	return nil
}

func (a *Assertion) validate() error {
	if !ops[a.Op] {
		return fmt.Errorf("unknown op %q", a.Op)
	}
	if !targets[a.Target] {
		return fmt.Errorf("unknown target %q", a.Target)
	}
	for _, p := range []string{a.In, a.Out} {
		if p != "" && !pathRe.MatchString(p) {
			return fmt.Errorf("bad path %q", p)
		}
	}
	switch a.Op {
	case "present", "absent":
		if a.Out == "" {
			return fmt.Errorf("%s needs out", a.Op)
		}
	case "equals":
		if a.Out == "" || a.Value == nil {
			return fmt.Errorf("equals needs out and value")
		}
	case "preserved":
		if a.In == "" || a.Out == "" {
			return fmt.Errorf("preserved needs in and out")
		}
	case "type_is":
		v, _ := a.Value.(string)
		if a.Out == "" || !jsonTypes[v] {
			return fmt.Errorf("type_is needs out and a JSON type name as value")
		}
	case "contains_anywhere", "absent_anywhere":
		if a.Text == "" {
			return fmt.Errorf("%s needs text", a.Op)
		}
	case "any_of", "all_of":
		if len(a.Items) < 2 {
			return fmt.Errorf("%s needs at least two items", a.Op)
		}
		for i := range a.Items {
			if err := a.Items[i].validate(); err != nil {
				return fmt.Errorf("items[%d]: %w", i, err)
			}
		}
	}
	return nil
}
