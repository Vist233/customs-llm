package redact

import (
	"strings"
	"testing"
)

func TestHeadersAndText(t *testing.T) {
	h := Headers(map[string]string{"Authorization": "Bearer sk-abcdefghijklmnop1234", "x-api-key": "sk-ant-abcdefghijklmnop", "content-type": "application/json", "x-note": "key sk-abcdefghijklmnopqrst here"})
	if strings.Contains(h["Authorization"], "abcdefghijkl") || strings.Contains(h["x-api-key"], "ghijkl") {
		t.Errorf("not masked: %v", h)
	}
	if h["content-type"] != "application/json" {
		t.Error("harmless header changed")
	}
	if strings.Contains(h["x-note"], "mnopqrst") {
		t.Errorf("embedded key not masked: %q", h["x-note"])
	}
}

func TestJSON(t *testing.T) {
	out := string(JSON([]byte(`{"api_key":"sk-realrealrealreal1234","max_tokens":10,"usage":{"input_tokens":3},"messages":[{"content":"my key is sk-zzzzzzzzzzzzzzzzzz99"}],"azure_ad_token":"eyJhbGciOi.abcdefghij.klmnopqrst"}`)))
	for _, leak := range []string{"realrealreal", "zzzzzzzzzzzz", "klmnopqrst"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in %s", leak, out)
		}
	}
	for _, keep := range []string{`"max_tokens":10`, `"input_tokens":3`} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q in %s", keep, out)
		}
	}
}
