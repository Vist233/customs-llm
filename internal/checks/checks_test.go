package checks

import (
	"encoding/json"
	"testing"

	"github.com/Vist233/customs-llm/internal/testset"
)

func decode(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Every case that ships examples must fail on bad and pass on good.
func TestExamplesSelfCheck(t *testing.T) {
	cases, err := testset.Load("../../testset/cases")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := range cases {
		c := &cases[i]
		if c.Examples == nil {
			continue
		}
		n++
		var inbound any
		_ = json.Unmarshal(c.Inbound.Body, &inbound)
		obs := func(raw json.RawMessage) *Observation {
			o := &Observation{Source: inbound}
			v := decode(t, raw)
			if c.Direction == "request" {
				o.UpstreamRequest = v
			} else {
				o.ClientResponse = v
			}
			return o
		}
		if len(c.Examples.Bad) > 0 && EvaluateCase(c, obs(c.Examples.Bad)).Pass {
			t.Errorf("%s: assertions pass on the bad example", c.ID)
		}
		if len(c.Examples.Good) > 0 {
			if r := EvaluateCase(c, obs(c.Examples.Good)); !r.Pass {
				t.Errorf("%s: assertions fail on the good example: %s", c.ID, r.Detail)
			}
		}
	}
	if n == 0 {
		t.Skip("no case has examples")
	}
}

func TestOps(t *testing.T) {
	msg := decode(t, json.RawMessage(`{"a":{"b":[{"c":1},{"c":2}]},"s":"hello <x> world","z":0}`))
	o := &Observation{Source: msg, UpstreamRequest: msg}
	cases := []struct {
		a    testset.Assertion
		want bool
	}{
		{testset.Assertion{Op: "present", Out: "$.a.b[*].c"}, true},
		{testset.Assertion{Op: "absent", Out: "$.a.q"}, true},
		{testset.Assertion{Op: "equals", Out: "$.z", Value: float64(0)}, true},
		{testset.Assertion{Op: "equals", Out: "$.a.b[*].c", Value: float64(1)}, false},
		{testset.Assertion{Op: "type_is", Out: "$.s", Value: "string"}, true},
		{testset.Assertion{Op: "contains_anywhere", Text: "<x>"}, true},
		{testset.Assertion{Op: "absent_anywhere", Text: "nope"}, true},
		{testset.Assertion{Op: "ordered_text", Texts: []string{"hello", "world"}}, true},
		{testset.Assertion{Op: "ordered_text", Texts: []string{"world", "hello"}}, false},
		{testset.Assertion{Op: "preserved", In: "$.a.b[0].c", Out: "$.a.b[*].c"}, true},
		{testset.Assertion{Op: "any_of", Items: []testset.Assertion{{Op: "present", Out: "$.q"}, {Op: "present", Out: "$.z"}}}, true},
		{testset.Assertion{Op: "all_of", Items: []testset.Assertion{{Op: "present", Out: "$.q"}, {Op: "present", Out: "$.z"}}}, false},
	}
	for _, c := range cases {
		if got := Evaluate(c.a, "request", o); got.Pass != c.want {
			t.Errorf("%+v: got %v (%s), want %v", c.a, got.Pass, got.Detail, c.want)
		}
	}
	if r := Evaluate(testset.Assertion{Op: "present", Out: "$.x", Target: "client_response"}, "request", o); r.Pass {
		t.Error("missing target must fail")
	}
}
