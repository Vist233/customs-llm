package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Vist233/customs-llm/internal/fakeup"
	"github.com/Vist233/customs-llm/internal/ir"
	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/probe"
	"github.com/Vist233/customs-llm/internal/proto"
	"github.com/Vist233/customs-llm/internal/relay"
	"github.com/Vist233/customs-llm/internal/store"
)

func cmdFakeup(args []string) error {
	fs := flag.NewFlagSet("fakeup", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:9100", "address to listen on")
	_ = fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fake := fakeup.New()
	fake.Log = os.Stderr
	srv := &http.Server{Addr: *listen, Handler: fake.Handler()}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	fmt.Fprintln(os.Stderr, "fake upstream listening on", *listen)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func cmdProbe(args []string) error {
	if len(args) == 0 || args[0] != "run" {
		return fmt.Errorf("usage: customs probe run --url GATEWAY_URL [flags]")
	}
	fs := flag.NewFlagSet("probe run", flag.ExitOnError)
	name := fs.String("name", "gateway", "name of the gateway under test, used in the report")
	url := fs.String("url", "", "base URL of the gateway, e.g. http://localhost:4000")
	key := fs.String("key", os.Getenv("CUSTOMS_GATEWAY_KEY"), "gateway API key (default: $CUSTOMS_GATEWAY_KEY)")
	listen := fs.String("listen", "127.0.0.1:9100", "address of the fake upstream the gateway must be configured to use")
	chat := fs.String("chat", "customs-chat", "gateway model that routes to the fake upstream over OpenAI Chat")
	responses := fs.String("responses", "customs-responses", "gateway model that routes to the fake upstream over OpenAI Responses")
	anthropic := fs.String("anthropic", "customs-anthropic", "gateway model that routes to the fake upstream over Anthropic Messages")
	var aliases multiFlag
	fs.Var(&aliases, "alias", `extra route, repeatable: "<client protocol>-><upstream protocol>=<model>" or "route:<provider word>=<model>", e.g. openai_responses->openai_chat=customs-chat-bridge`)
	only := fs.String("case", "", "comma-separated case ids to run (default: all)")
	var satisfied multiFlag
	fs.Var(&satisfied, "satisfied", "case id whose preconditions the gateway setup already meets (repeatable)")
	pre := fs.Bool("with-preconditions", false, "also run cases that need special gateway configuration")
	dir := fs.String("testset", "", "load cases from this directory instead of the embedded test set")
	dump := fs.Bool("dump", false, "include what was captured for each case in the JSON results")
	record := fs.Bool("record", true, "record each executed case (client request and the bytes the gateway sent upstream) in the data directory")
	dataDir := fs.String("data", defaultDataDir(), "data directory used by --record")
	asJSON := fs.Bool("json", false, "print results as JSON")
	out := fs.String("out", "", "also write the JSON results to this file")
	_ = fs.Parse(args[1:])
	if *url == "" {
		return fmt.Errorf("--url is required")
	}
	cases, err := loadTestset(*dir)
	if err != nil {
		return err
	}
	fake := fakeup.New()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: fake.Handler()}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	sel := map[string]bool{}
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			sel[id] = true
		}
	}
	routes := map[string]string{"*->openai_chat": *chat, "*->openai_responses": *responses, "*->anthropic_messages": *anthropic}
	for _, a := range aliases {
		k, v, ok := strings.Cut(a, "=")
		if !ok || !(strings.Contains(k, "->") || strings.HasPrefix(k, "route:") || strings.HasPrefix(k, "path:")) {
			return fmt.Errorf("bad --alias %q", a)
		}
		routes[k] = v
	}
	sat := map[string]bool{}
	for _, id := range satisfied {
		sat[id] = true
	}
	r := probe.NewRunner(probe.Target{Name: *name, BaseURL: *url, Key: *key, WithPreconditions: *pre, Satisfied: sat, Aliases: routes}, fake)
	r.Dump = *dump
	if *record {
		st, err := store.Open(*dataDir)
		if err != nil {
			return fmt.Errorf("opening data directory for --record: %w", err)
		}
		defer st.Close()
		r.Sink = storeSink(st, *name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	results := r.RunAll(ctx, cases, sel)
	if *out != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(results)
	}
	counts := map[probe.Outcome]int{}
	for _, res := range results {
		counts[res.Outcome]++
		fmt.Printf("%-12s %-58s %s\n", res.Outcome, res.ID, res.Detail)
	}
	fmt.Printf("\n%d cases: PASS=%d FAIL=%d INCONCLUSIVE=%d SKIP=%d\n", len(results),
		counts[probe.Pass], counts[probe.Fail], counts[probe.Inconclusive], counts[probe.Skip])
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// storeSink keeps each executed case as a two-hop trace: what the probe sent to the gateway and what the
// gateway then sent to the fake upstream. That is the exact-bytes comparison the reconcile table shows.
func storeSink(st *store.Store, gateway string) func(probe.Run) {
	return func(run probe.Run) {
		ctx := context.Background()
		idb := make([]byte, 8)
		_, _ = rand.Read(idb)
		id := hex.EncodeToString(idb)
		c := run.Case

		stream := strings.Contains(run.ClientType, "event-stream")
		var evs []model.Event
		if stream {
			evs = relay.ParseStream(run.ClientBody)
		}
		cl := ir.ReadResponse(proto.OfPath(c.Inbound.Path), run.ClientStatus, run.ClientBody, evs)
		h1 := &model.Hop{TraceID: id, Seq: 0, Source: "probe", Tier: model.TierWire, From: "probe", To: gateway, Started: run.SentAt,
			Ended: run.SentAt.Add(run.Elapsed), Protocol: proto.OfPath(c.Inbound.Path), Method: c.Inbound.Method, URI: c.Inbound.Path,
			ReqHeader: map[string]string{"content-type": "application/json"}, ReqBody: run.Sent, Status: run.ClientStatus,
			RespHdr: map[string]string{"content-type": run.ClientType}, RespBody: run.ClientBody, Streaming: stream, Model: cl.Model,
			Finish: cl.Finish, Usage: cl.Usage, Events: len(evs), Note: map[string]string{"case": c.ID, "outcome": string(run.Result.Outcome)}}
		_ = st.SaveHop(ctx, h1)
		for i := range evs {
			evs[i].HopID = h1.ID
		}
		_ = st.AddEvents(ctx, evs)

		up := run.Upstream
		upStream := strings.Contains(up.RespType, "event-stream") || strings.Contains(up.RespType, "ndjson")
		var uevs []model.Event
		if upStream {
			uevs = relay.ParseStream(up.RespBody)
		}
		ur := ir.ReadResponse(up.Protocol, up.Status, up.RespBody, uevs)
		h2 := &model.Hop{TraceID: id, Seq: 1, Source: "fakeup", Tier: model.TierWire, From: gateway, To: "fakeup", Started: up.At,
			Ended: up.At.Add(up.RespElapsed), Protocol: up.Protocol, Method: up.Method, URI: up.URI, ReqHeader: up.Header, ReqBody: up.Body,
			Status: up.Status, RespHdr: map[string]string{"content-type": up.RespType}, RespBody: up.RespBody, Streaming: upStream,
			Model: ur.Model, Finish: ur.Finish, Usage: ur.Usage, Events: len(uevs), Note: map[string]string{"case": c.ID}}
		_ = st.SaveHop(ctx, h2)
		for i := range uevs {
			uevs[i].HopID = h2.ID
		}
		_ = st.AddEvents(ctx, uevs)
		_ = st.SetFlags(ctx, id, []string{c.ID, string(run.Result.Outcome)})
	}
}
