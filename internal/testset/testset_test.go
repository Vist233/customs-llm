package testset

import "testing"

func TestShippedCasesAreValid(t *testing.T) {
	cases, err := Load("../../testset/cases")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases loaded")
	}
	t.Logf("%d cases", len(cases))
}

func TestValidateRejects(t *testing.T) {
	good := func() Case {
		return Case{
			ID: "x-1", Title: "t",
			Source:   Source{Repo: "a/b", Issue: 1, URL: "https://example.com"},
			Gateway:  Gateway{Name: "g"},
			Category: "field_dropped", Direction: "request",
			Protocol: Protocol{In: "openai_chat", Out: "openai_chat"},
			Severity: "low", Detectable: "yes", PayloadOrigin: "issue",
			Inbound:    Request{Method: "POST", Path: "/v1/chat/completions", Body: []byte(`{}`)},
			Assertions: []Assertion{{Op: "present", Out: "$.a"}},
			Observed:   "o",
		}
	}
	c := good()
	if err := c.Validate(); err != nil {
		t.Fatalf("good case rejected: %v", err)
	}
	for name, mut := range map[string]func(*Case){
		"bad category":   func(c *Case) { c.Category = "nope" },
		"bad path":       func(c *Case) { c.Assertions[0].Out = "a.b" },
		"stream no mock": func(c *Case) { c.Direction = "stream" },
		"any_of too small": func(c *Case) {
			c.Assertions = []Assertion{{Op: "any_of", Items: []Assertion{{Op: "present", Out: "$.a"}}}}
		},
		"bad type_is": func(c *Case) { c.Assertions = []Assertion{{Op: "type_is", Out: "$.a", Value: "str"}} },
	} {
		c := good()
		mut(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
