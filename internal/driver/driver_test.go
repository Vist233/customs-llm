package driver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got, err := LoadManifest(dir); err != nil || got != nil {
		t.Fatalf("empty manifest: %v %v", got, err)
	}
	_ = Remember(dir, Override{PlanID: "a", Driver: "litellm", Routes: []Route{{Ref: "x"}}})
	_ = Remember(dir, Override{PlanID: "b", Driver: "magpie"})
	if got, _ := LoadManifest(dir); len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	_ = Forget(dir, "a")
	got, _ := LoadManifest(dir)
	if len(got) != 1 || got[0].PlanID != "b" {
		t.Fatalf("got %+v", got)
	}
	_ = Forget(dir, "b")
	if got, _ := LoadManifest(dir); got != nil {
		t.Fatalf("not removed: %+v", got)
	}
}

func TestLiteLLMAddRemove(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		paths, bodies, auth = append(paths, r.URL.Path), append(bodies, m), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	d := &LiteLLM{URL: ts.URL, Key: "k"}
	routes := d.Plan("http://fake:1", "ab")
	for _, r := range routes {
		if !strings.HasPrefix(r.Model, "customs-probe-ab-") {
			t.Fatalf("entry not namespaced: %s", r.Model)
		}
	}
	if err := d.Add(context.Background(), routes[0]); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove(context.Background(), routes[0]); err != nil {
		t.Fatal(err)
	}
	if paths[0] != "/model/new" || paths[1] != "/model/delete" || auth != "Bearer k" || bodies[1]["id"] != routes[0].Ref {
		t.Fatalf("%v %v %s", paths, bodies, auth)
	}
}

func TestLiteLLMErrorSurfaces(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"no db"}`, 400)
	}))
	defer ts.Close()
	d := &LiteLLM{URL: ts.URL}
	if err := d.Add(context.Background(), d.Plan("http://f", "x")[0]); err == nil || !strings.Contains(err.Error(), "no db") {
		t.Fatalf("err: %v", err)
	}
}

func TestMagpieArgsSurviveJSON(t *testing.T) {
	m := &Magpie{Command: []string{"magpie"}}
	r := m.Plan("http://f:1", "ab")[2]
	b, _ := json.Marshal(r)
	var back Route
	_ = json.Unmarshal(b, &back)
	if !strings.Contains(m.Describe(back), "anthropic=http://f:1") {
		t.Fatalf("%s", m.Describe(back))
	}
}
