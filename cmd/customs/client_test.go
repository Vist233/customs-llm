package main

import "testing"

func TestOpenAIBase(t *testing.T) {
	base := "http://127.0.0.1:9000/go"
	cases := map[string]string{
		"https://opencode.ai/inference/go/openai/v1":  base,
		"https://opencode.ai/inference/go/openai/v1/": base,
		"https://opencode.ai/inference/go/openai":     base + "/v1",
		"": base + "/v1",
	}
	for upstream, want := range cases {
		if got := openaiBase(base, upstream); got != want {
			t.Errorf("openaiBase(%q) = %q, want %q", upstream, got, want)
		}
	}
}
