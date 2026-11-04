package collect

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// The shape below was copied from a real opencode 1.19.5 database.
func TestOpenCodeImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", store.URI(path, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE message(id TEXT, session_id TEXT, time_created INT, time_updated INT, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	ins := func(id string, up int64, data string) {
		if _, err := db.Exec(`INSERT INTO message VALUES(?,?,?,?,?)`, id, "ses1", up, up, data); err != nil {
			t.Fatal(err)
		}
	}
	ins("u1", 1000, `{"role":"user","time":{"created":1000}}`)
	ins("a1", 1100, `{"role":"assistant","providerID":"fake","modelID":"bad","time":{"created":1000,"completed":1100},"tokens":{"input":0,"output":0,"cache":{}},"error":{"name":"APIError","data":{"message":"Bad Request","statusCode":400}}}`)
	ins("a2", 1200, `{"role":"assistant","providerID":"go","modelID":"ok","finish":"stop","time":{"created":1000,"completed":1200},"tokens":{"input":3,"output":1,"cache":{"read":2,"write":0}}}`)
	ins("a3", 1300, `{"role":"assistant","providerID":"go","modelID":"unfinished","time":{"created":1000}}`)
	st := newStore(t)
	ctx := context.Background()
	c := &OpenCode{Path: path, Store: st}
	n, err := c.Poll(ctx)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if n, _ := c.Poll(ctx); n != 0 {
		t.Fatalf("re-imported %d", n)
	}
	bad, _ := st.Trace(ctx, "oc-a1")
	if bad == nil || bad.State != "http_error" || bad.Status != 400 || bad.Client != "opencode" || bad.Target != "fake" {
		t.Fatalf("bad: %+v", bad)
	}
	ok, _ := st.Trace(ctx, "oc-a2")
	if ok.State != "ok" || ok.Usage.In != 3 || ok.Usage.CacheRead != 2 || ok.Finish != "stop" {
		t.Fatalf("ok: %+v", ok)
	}
	hops, _ := st.Hops(ctx, "oc-a2")
	if hops[0].Tier != model.TierReported {
		t.Fatalf("tier: %s", hops[0].Tier)
	}
	// a message finished later is picked up on a later poll
	if _, err := db.Exec(`UPDATE message SET time_updated=1400, data=? WHERE id='a3'`, `{"role":"assistant","providerID":"go","modelID":"unfinished","time":{"created":1000,"completed":1400},"tokens":{"cache":{}}}`); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.Poll(ctx); n != 1 {
		t.Fatalf("later poll: %d", n)
	}
}

func TestOpenCodeMissingDB(t *testing.T) {
	c := &OpenCode{Path: filepath.Join(t.TempDir(), "none.db"), Store: newStore(t)}
	if _, err := c.Poll(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

// The row shape was copied from a real LiteLLM 1.108.0 /spend/logs/v2 reply.
func TestLiteLLMJoinsWireTrace(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	wire := &model.Hop{TraceID: "t1", Source: "relay", Tier: model.TierWire, From: "client", To: "litellm", Started: time.Now(), Ended: time.Now(), Status: 200, Usage: model.UnknownUsage()}
	if err := st.SaveHop(ctx, wire); err != nil {
		t.Fatal(err)
	}
	_ = st.Link(ctx, "litellm:call-1", "t1")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spend/logs/v2" || r.Header.Get("Authorization") != "Bearer k" || r.URL.Query().Get("start_date") == "" {
			http.Error(w, "bad", 400)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
		 {"request_id":"chatcmpl-1","litellm_call_id":"call-1","call_type":"acompletion","model":"openai/x","model_group":"x","custom_llm_provider":"openai","api_base":"http://u/v1","status":"success","startTime":"2026-11-04T08:22:05.926+00:00","completionStartTime":"2026-11-04T08:22:05.928+00:00","endTime":"2026-11-04T08:22:06.000+00:00","prompt_tokens":3,"completion_tokens":1,"proxy_server_request":null,"response":null},
		 {"request_id":"call-2","litellm_call_id":"call-2","call_type":"acompletion","model":"openai/y","status":"failure","startTime":"2026-11-04T08:22:05.926+00:00","endTime":"2026-11-04T08:22:05.970+00:00","proxy_server_request":{},"response":{}}
		],"total_pages":1}`))
	}))
	defer ts.Close()
	c := &LiteLLM{URL: ts.URL, Key: "k", Store: st}
	n, err := c.Poll(ctx)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if n, _ := c.Poll(ctx); n != 0 {
		t.Fatalf("re-imported %d", n)
	}
	hops, _ := st.Hops(ctx, "t1")
	if len(hops) != 2 || hops[1].Tier != model.TierReported || hops[1].Usage.In != 3 || hops[1].Note["call_type"] != "acompletion" {
		t.Fatalf("hops: %+v", hops)
	}
	if tr, _ := st.Trace(ctx, "t1"); tr.Hops != 2 || tr.Target != "litellm" {
		t.Fatalf("wire hop must stay primary: %+v", tr)
	}
	if tr, _ := st.Trace(ctx, "ll-call-2"); tr == nil || tr.State != "transport_error" {
		t.Fatalf("failure row: %+v", tr)
	}
}
