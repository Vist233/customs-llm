// Package checks evaluates test set assertions against what was observed at a gateway boundary.
package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Vist233/customs-llm/internal/jsonq"
	"github.com/Vist233/customs-llm/internal/testset"
)

// Observation is everything a run captured for one case. Any field may be nil when it was not captured.
type Observation struct {
	// Source is the message the gateway received: the inbound body for request cases, the mock
	// upstream reply for response cases. It is what "preserved" assertions compare against.
	Source          any
	UpstreamRequest any
	UpstreamHeaders map[string]string
	UpstreamURL     string
	ClientResponse  any
}

// Result is the outcome of one assertion or one case.
type Result struct {
	Pass   bool
	Detail string
}

func (o *Observation) target(name string, direction string) (any, string, bool) {
	if name == "" {
		name = "upstream_request"
		if direction != "request" {
			name = "client_response"
		}
	}
	switch name {
	case "upstream_request":
		return o.UpstreamRequest, name, o.UpstreamRequest != nil
	case "upstream_headers":
		if o.UpstreamHeaders == nil {
			return nil, name, false
		}
		m := make(map[string]any, len(o.UpstreamHeaders))
		for k, v := range o.UpstreamHeaders {
			m[k] = v
		}
		return m, name, true
	case "upstream_url":
		return o.UpstreamURL, name, o.UpstreamURL != ""
	case "client_response":
		return o.ClientResponse, name, o.ClientResponse != nil
	}
	return nil, name, false
}

// Compact renders v as compact JSON with sorted keys and without HTML escaping.
func Compact(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// Evaluate checks one assertion. direction is the case direction.
func Evaluate(a testset.Assertion, direction string, o *Observation) Result {
	switch a.Op {
	case "any_of":
		var details []string
		for _, it := range a.Items {
			r := Evaluate(it, direction, o)
			if r.Pass {
				return Result{true, "any_of: " + r.Detail}
			}
			details = append(details, r.Detail)
		}
		return Result{false, "none matched: " + strings.Join(details, "; ")}
	case "all_of":
		for _, it := range a.Items {
			if r := Evaluate(it, direction, o); !r.Pass {
				return r
			}
		}
		return Result{true, "all matched"}
	}
	msg, tname, ok := o.target(a.Target, direction)
	if !ok {
		return Result{false, fmt.Sprintf("target %s was not captured", tname)}
	}
	switch a.Op {
	case "contains_anywhere":
		if strings.Contains(Compact(msg), a.Text) {
			return Result{true, fmt.Sprintf("%s contains %q", tname, a.Text)}
		}
		return Result{false, fmt.Sprintf("%s does not contain %q", tname, a.Text)}
	case "absent_anywhere":
		if strings.Contains(Compact(msg), a.Text) {
			return Result{false, fmt.Sprintf("%s contains %q", tname, a.Text)}
		}
		return Result{true, fmt.Sprintf("%s has no %q", tname, a.Text)}
	case "ordered_text":
		s, from := Compact(msg), 0
		for _, t := range a.Texts {
			i := strings.Index(s[from:], t)
			if i < 0 {
				return Result{false, fmt.Sprintf("%q not found in order after offset %d", t, from)}
			}
			from += i + len(t)
		}
		return Result{true, "texts appear in order"}
	case "present", "absent", "equals", "type_is":
		got, err := jsonq.Select(msg, a.Out)
		if err != nil {
			return Result{false, err.Error()}
		}
		switch a.Op {
		case "present":
			return Result{len(got) > 0, fmt.Sprintf("%s matched %d value(s)", a.Out, len(got))}
		case "absent":
			return Result{len(got) == 0, fmt.Sprintf("%s matched %d value(s)", a.Out, len(got))}
		case "equals":
			if len(got) == 0 {
				return Result{false, a.Out + " is missing"}
			}
			for _, g := range got {
				if !reflect.DeepEqual(g, a.Value) {
					return Result{false, fmt.Sprintf("%s = %s, want %s", a.Out, Compact(g), Compact(a.Value))}
				}
			}
			return Result{true, a.Out + " equals expected value"}
		default:
			if len(got) == 0 {
				return Result{false, a.Out + " is missing"}
			}
			want, _ := a.Value.(string)
			for _, g := range got {
				if jsonq.TypeName(g) != want {
					return Result{false, fmt.Sprintf("%s is %s, want %s", a.Out, jsonq.TypeName(g), want)}
				}
			}
			return Result{true, a.Out + " has type " + want}
		}
	case "preserved":
		in, err := jsonq.Select(o.Source, a.In)
		if err != nil {
			return Result{false, err.Error()}
		}
		if len(in) == 0 {
			return Result{false, a.In + " is missing from the source message (case bug)"}
		}
		out, err := jsonq.Select(msg, a.Out)
		if err != nil {
			return Result{false, err.Error()}
		}
		for _, want := range in {
			found := false
			for _, g := range out {
				if reflect.DeepEqual(g, want) {
					found = true
					break
				}
			}
			if !found {
				return Result{false, fmt.Sprintf("%s = %s was not found at %s", a.In, Compact(want), a.Out)}
			}
		}
		return Result{true, a.In + " preserved at " + a.Out}
	}
	return Result{false, "unknown op " + a.Op}
}

// EvaluateCase checks every assertion of a case; it passes only if all of them do.
func EvaluateCase(c *testset.Case, o *Observation) Result {
	var details []string
	pass := true
	for _, a := range c.Assertions {
		r := Evaluate(a, c.Direction, o)
		if !r.Pass {
			pass = false
		}
		details = append(details, r.Detail)
	}
	return Result{pass, strings.Join(details, "; ")}
}
