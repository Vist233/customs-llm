package store

import (
	"context"
	"testing"
	"time"

	"github.com/Vist233/customs-llm/internal/model"
)

func TestHopLifecycle(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	h := &model.Hop{TraceID: "t1", Source: "relay", Tier: model.TierWire, From: "client", To: "litellm", Started: now, Method: "POST",
		URI: "/v1/chat/completions", Protocol: "openai_chat", ReqBody: []byte(`{"model":"m"}`), ReqHeader: map[string]string{"a": "b"}, Usage: model.UnknownUsage()}
	if err := s.SaveHop(ctx, h); err != nil || h.ID == 0 {
		t.Fatalf("save: %v id=%d", err, h.ID)
	}
	if tr, _ := s.Trace(ctx, "t1"); tr == nil || tr.State != "running" {
		t.Fatalf("expected running, got %+v", tr)
	}
	h.Ended, h.Status, h.RespBody, h.Usage = now.Add(80*time.Millisecond), 200, []byte(`{"ok":true}`), model.Usage{In: 3, Out: 1, CacheRead: -1, CacheWrite: -1, Reasoning: -1}
	if err := s.SaveHop(ctx, h); err != nil {
		t.Fatal(err)
	}
	tr, _ := s.Trace(ctx, "t1")
	if tr.State != "ok" || tr.Millis != 80 || tr.Usage.In != 3 || tr.Target != "litellm" {
		t.Fatalf("summary: %+v", tr)
	}
	hops, _ := s.Hops(ctx, "t1")
	if len(hops) != 1 || string(hops[0].ReqBody) != `{"model":"m"}` || string(hops[0].RespBody) != `{"ok":true}` || hops[0].ReqHeader["a"] != "b" {
		t.Fatalf("hops: %+v", hops)
	}
	if err := s.AddEvents(ctx, []model.Event{{HopID: h.ID, Idx: 0, OffsetUS: 10, Kind: "data", Data: []byte(`{"x":1}`)}, {HopID: h.ID, Idx: 1, Kind: "comment", Data: []byte(": ka")}}); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.Events(ctx, h.ID)
	if len(evs) != 2 || string(evs[0].Data) != `{"x":1}` || evs[1].Kind != "comment" {
		t.Fatalf("events: %+v", evs)
	}
	list, _ := s.ListTraces(ctx, Filter{State: "failed"})
	if len(list) != 0 {
		t.Fatalf("failed filter: %+v", list)
	}
	if list, _ := s.ListTraces(ctx, Filter{Query: "chat"}); len(list) != 1 {
		t.Fatalf("query filter: %+v", list)
	}
	_ = s.Link(ctx, "call-1", "t1")
	if id, _ := s.TraceByKey(ctx, "call-1"); id != "t1" {
		t.Fatalf("corr: %q", id)
	}
}

func TestBlobDedupe(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	a, _ := s.PutBlob([]byte("same"))
	b, _ := s.PutBlob([]byte("same"))
	if a != b || a == "" {
		t.Fatalf("dedupe: %q %q", a, b)
	}
	if got, _ := s.Blob(a); string(got) != "same" {
		t.Fatalf("blob: %q", got)
	}
	if _, err := s.Blob("../x"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
