package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vist233/customs-llm/internal/fakeup"
	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/store"
	"github.com/Vist233/customs-llm/internal/testset"
)

func setup(t *testing.T) (*Relay, *fakeup.Server, *httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fake := fakeup.New()
	up := httptest.NewServer(fake.Handler())
	t.Cleanup(up.Close)
	r := New(st)
	if err := r.SetTarget("up", up.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	rs := httptest.NewServer(r)
	t.Cleanup(rs.Close)
	return r, fake, rs, st
}

// settled waits until no trace is still running: the relay finalises a hop just after the client has its answer.
func settled(t *testing.T, st *store.Store, f store.Filter) []model.TraceSummary {
	t.Helper()
	for i := 0; i < 100; i++ {
		list, _ := st.ListTraces(context.Background(), f)
		running := false
		for _, tr := range list {
			if tr.State == "running" {
				running = true
			}
		}
		if !running && len(list) > 0 {
			return list
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("trace never settled")
	return nil
}

func TestRelayRecordsAndForwards(t *testing.T) {
	_, fake, rs, st := setup(t)
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", rs.URL+"/up/chat/completions?x=1", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-secretsecretsecret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(got), "chat.completion") {
		t.Fatalf("client got %d %s", resp.StatusCode, got)
	}
	recs := fake.Records()
	if len(recs) != 1 || string(recs[0].Body) != body || recs[0].URI != "/v1/chat/completions?x=1" || recs[0].Header["authorization"] != "Bearer sk-secretsecretsecret" || recs[0].Header["x-customs-trace"] == "" {
		t.Fatalf("upstream saw: %+v", recs)
	}
	list := settled(t, st, store.Filter{})
	if len(list) != 1 || list[0].State != "ok" || list[0].Usage.In != 3 || list[0].Target != "up" || list[0].Model != "m" || list[0].Finish != "stop" {
		t.Fatalf("trace: %+v", list)
	}
	hops, _ := st.Hops(context.Background(), list[0].ID)
	if string(hops[0].ReqBody) != body || hops[0].ReqHeader["authorization"] == "" || !strings.Contains(string(hops[0].RespBody), "chat.completion") {
		t.Fatalf("hop: %+v", hops[0])
	}
}

func TestRelayStreamsAndRecordsEvents(t *testing.T) {
	_, fake, rs, st := setup(t)
	fake.SetScript(&testset.MockUpstream{Status: 200, SSE: []string{`{"choices":[{"delta":{"content":"a"}}]}`, ": keepalive", `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, "[DONE]"}})
	resp, err := http.Post(rs.URL+"/up/chat/completions", "application/json", strings.NewReader(`{"model":"m","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n: keepalive\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n" {
		t.Fatalf("stream was altered: %q", got)
	}
	list := settled(t, st, store.Filter{})
	if len(list) != 1 || !list[0].Streaming || list[0].Finish != "stop" || list[0].State != "ok" {
		t.Fatalf("trace: %+v", list)
	}
	hops, _ := st.Hops(context.Background(), list[0].ID)
	evs, _ := st.Events(context.Background(), hops[0].ID)
	if len(evs) != 4 || evs[1].Kind != "comment" || string(evs[3].Data) != "[DONE]" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestRelayKeepsUpstreamErrorBody(t *testing.T) {
	_, fake, rs, st := setup(t)
	fake.SetScript(&testset.MockUpstream{Status: 400, Body: []byte(`{"error":{"message":"context too long","code":"context_length_exceeded"}}`)})
	resp, _ := http.Post(rs.URL+"/up/chat/completions", "application/json", strings.NewReader(`{"model":"m"}`))
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 || !strings.Contains(string(got), "context too long") {
		t.Fatalf("client got %d %s", resp.StatusCode, got)
	}
	settled(t, st, store.Filter{})
	list, _ := st.ListTraces(context.Background(), store.Filter{State: "http_error"})
	if len(list) != 1 {
		t.Fatalf("http_error trace missing: %+v", list)
	}
}

func TestRelayTransportError(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	r := New(st)
	_ = r.SetTarget("dead", "http://127.0.0.1:1")
	rs := httptest.NewServer(r)
	defer rs.Close()
	resp, _ := http.Post(rs.URL+"/dead/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	settled(t, st, store.Filter{})
	list, _ := st.ListTraces(context.Background(), store.Filter{State: "transport_error"})
	if len(list) != 1 {
		t.Fatalf("%+v", list)
	}
}

func TestRelayUnknownTarget(t *testing.T) {
	_, _, rs, _ := setup(t)
	resp, _ := http.Get(rs.URL + "/nope/x")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || !strings.Contains(string(b), `"targets"`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestOnChangeAndTiming(t *testing.T) {
	r, fake, rs, _ := setup(t)
	var n int
	r.OnChange = func(string) { n++ }
	fake.SetScript(&testset.MockUpstream{Status: 200, Body: []byte(`{}`)})
	start := time.Now()
	resp, _ := http.Post(rs.URL+"/up/chat/completions", "application/json", strings.NewReader(`{}`))
	resp.Body.Close()
	if n < 2 || time.Since(start) > 5*time.Second {
		t.Fatalf("onchange=%d", n)
	}
	_ = model.TierWire
}

func TestRelayPassesFaultsThrough(t *testing.T) {
	_, _, rs, st := setup(t)
	post := func(model string) (*http.Response, error) {
		return http.Post(rs.URL+"/up/chat/completions", "application/json", strings.NewReader(`{"model":"`+model+`","stream":true}`))
	}
	if _, err := post("fault-reset"); err == nil {
		t.Error("client should see the connection drop, not a 502")
	}
	resp, err := post("fault-reset-midstream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("client should see the stream break, not a clean end")
	}
	resp.Body.Close()
	resp, err = post("fault-no-terminal")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	settled(t, st, store.Filter{})
	byModel := map[string]string{}
	list, _ := st.ListTraces(context.Background(), store.Filter{})
	for _, tr := range list {
		byModel[tr.Model] = tr.State
	}
	want := map[string]string{"fault-reset": "transport_error", "fault-reset-midstream": "transport_error", "fault-no-terminal": "incomplete"}
	for m, s := range want {
		if byModel[m] != s {
			t.Errorf("%s: state %q, want %q (%v)", m, byModel[m], s, byModel)
		}
	}
}

func TestRelayFlagsRepeats(t *testing.T) {
	_, _, rs, st := setup(t)
	for i := 0; i < 3; i++ {
		resp, err := http.Post(rs.URL+"/up/chat/completions", "application/json", strings.NewReader(`{"model":"same"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	list := settled(t, st, store.Filter{})
	if len(list) != 3 || len(list[0].Flags) != 1 || list[0].Flags[0] != "相同请求第 3 次" || len(list[2].Flags) != 0 {
		t.Fatalf("%+v", list)
	}
}
