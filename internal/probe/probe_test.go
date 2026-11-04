package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vist233/customs-llm/internal/fakeup"
	"github.com/Vist233/customs-llm/internal/testset"
)

// A tiny gateway: forwards chat requests to the fake upstream and, when drop is set, deletes a field.
func gateway(upstream string, drop string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		delete(m, drop)
		nb, _ := json.Marshal(m)
		resp, err := http.Post(upstream+r.URL.Path, "application/json", strings.NewReader(string(nb)))
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

func TestRunDetectsDroppedField(t *testing.T) {
	c := testset.Case{
		ID: "x-1-drop", Title: "t", Source: testset.Source{Repo: "a/b", Issue: 1, URL: "https://x"},
		Gateway: testset.Gateway{Name: "x"}, Category: "field_dropped", Direction: "request",
		Protocol: testset.Protocol{In: "openai_chat", Out: "openai_chat"}, Severity: "low", Detectable: "yes", PayloadOrigin: "issue",
		Inbound:    testset.Request{Method: "POST", Path: "/v1/chat/completions", Body: []byte(`{"model":"x","messages":[],"reasoning_effort":"low"}`)},
		Assertions: []testset.Assertion{{Op: "preserved", In: "$.reasoning_effort", Out: "$.reasoning_effort"}}, Observed: "o",
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	fake := fakeup.New()
	up := httptest.NewServer(fake.Handler())
	defer up.Close()
	for drop, want := range map[string]Outcome{"": Pass, "reasoning_effort": Fail} {
		gw := httptest.NewServer(gateway(up.URL, drop))
		r := NewRunner(Target{Name: "sim", BaseURL: gw.URL, Aliases: map[string]string{"*->openai_chat": "customs-chat"}}, fake)
		got := r.Run(context.Background(), &c)
		gw.Close()
		if got.Outcome != want {
			t.Errorf("drop=%q: %s (%s), want %s", drop, got.Outcome, got.Detail, want)
		}
	}
}

func TestParseClientResponse(t *testing.T) {
	v := ParseClientResponse("text/event-stream", []byte("data: {\"a\":1}\n\n: keepalive\n\ndata: [DONE]\n\n")).(map[string]any)
	if len(v["events"].([]any)) != 2 || len(v["comments"].([]any)) != 1 {
		t.Errorf("%v", v)
	}
}
