package fakeup

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vist233/customs-llm/internal/testset"
)

func post(t *testing.T, url, body string) (int, string, string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

func TestDefaultsAndRecording(t *testing.T) {
	s := New()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for _, c := range [][2]string{
		{"/v1/chat/completions", "chat.completion"}, {"/v1/responses", `"object":"response"`}, {"/v1/messages", `"type":"message"`}} {
		path, want := c[0], c[1]
		code, ct, body := post(t, ts.URL+path, `{"model":"m","x":1}`)
		if code != 200 || !strings.Contains(ct, "json") || !strings.Contains(body, want) {
			t.Errorf("%s: %d %s %s", path, code, ct, body)
		}
	}
	_, ct, body := post(t, ts.URL+"/v1/messages", `{"model":"m","stream":true}`)
	if !strings.Contains(ct, "event-stream") || !strings.Contains(body, "event: message_start") || !strings.Contains(body, "event: message_stop") {
		t.Errorf("anthropic stream: %s %s", ct, body)
	}
	recs := s.Records()
	if len(recs) != 4 || recs[0].Protocol != Chat || string(recs[0].Body) != `{"model":"m","x":1}` {
		t.Errorf("records: %+v", recs)
	}
}

func TestScript(t *testing.T) {
	s := New()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	s.SetScript(&testset.MockUpstream{Status: 200, SSE: []string{`{"a":1}`, ": keepalive", "[DONE]"}})
	_, ct, body := post(t, ts.URL+"/v1/chat/completions", `{"stream":true}`)
	if !strings.Contains(ct, "event-stream") || body != "data: {\"a\":1}\n\n: keepalive\n\ndata: [DONE]\n\n" {
		t.Errorf("script stream: %q", body)
	}
	s.SetScript(&testset.MockUpstream{Status: 400, Body: []byte(`{"error":"bad"}`)})
	if code, _, body := post(t, ts.URL+"/v1/responses", `{}`); code != 400 || body != `{"error":"bad"}` {
		t.Errorf("script body: %d %s", code, body)
	}
}

func TestFaults(t *testing.T) {
	s := New()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	code, _, body := post(t, ts.URL+"/v1/chat/completions", `{"model":"fault-empty400"}`)
	if code != 400 || body != "" {
		t.Errorf("empty400: %d %q", code, body)
	}
	code, _, body = post(t, ts.URL+"/v1/chat/completions", `{"model":"fault-quota-text"}`)
	if code != 429 || !strings.Contains(body, "Weekly usage limit") {
		t.Errorf("quota: %d %q", code, body)
	}
	if _, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"fault-reset"}`)); err == nil {
		t.Error("reset: expected a transport error")
	}
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"fault-reset-midstream"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err == nil {
		t.Error("reset-midstream: expected a read error")
	}
}

func TestScriptedBodyIsStreamedWhenAsked(t *testing.T) {
	s := New()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	s.SetScript(&testset.MockUpstream{Status: 200, Body: []byte(`{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"length"}],"usage":{"prompt_tokens":1}}`)})
	_, ct, body := post(t, ts.URL+"/v1/chat/completions", `{"stream":true}`)
	if !strings.Contains(ct, "event-stream") || !strings.Contains(body, `"content":"hello"`) || !strings.Contains(body, `"finish_reason":"length"`) || !strings.Contains(body, "[DONE]") {
		t.Errorf("%s %s", ct, body)
	}
	_, ct, _ = post(t, ts.URL+"/v1/chat/completions", `{}`)
	if !strings.Contains(ct, "json") {
		t.Errorf("non-stream request got %s", ct)
	}
}
