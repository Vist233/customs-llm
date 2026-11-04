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
	"syscall"
	"time"

	"github.com/Vist233/customs-llm/internal/api"
	"github.com/Vist233/customs-llm/internal/driver"
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
	if len(args) == 0 {
		return &exitError{2, "usage: customs probe plan|run|cleanup"}
	}
	switch args[0] {
	case "plan":
		return cmdProbePlan(args[1:])
	case "cleanup":
		return cmdProbeCleanup(args[1:])
	case "run":
	default:
		return &exitError{2, "usage: customs probe plan|run|cleanup"}
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
	planID := fs.String("plan", "", "run an approved plan: add its temporary models, run, then remove them")
	asJSON := fs.Bool("json", false, "print results as JSON")
	out := fs.String("out", "", "also write the JSON results to this file")
	resultsFile := fs.String("results", "", "write a results file for testset/results (needs --gateway-version)")
	gwVersion := fs.String("gateway-version", "", "version of the gateway, recorded in --results")
	_ = fs.Parse(args[1:])
	var plan *api.Plan
	var routes0 []driver.Route
	if *planID != "" {
		c, err := newClient(*dataDir)
		if err != nil {
			return err
		}
		var p api.Plan
		if err := c.do("POST", "/api/plans/"+*planID+"/consume", nil, &p); err != nil {
			return &exitError{3, "计划 " + *planID + " 不是待运行状态(未批准、已拒绝或已用过),不会修改网关。请用户在界面批准: http://" + c.d.UI}
		}
		if err := json.Unmarshal(p.Routes, &routes0); err != nil {
			return err
		}
		plan, *url = &p, p.URL
	}
	if *url == "" {
		return &exitError{2, "--url or --plan is required"}
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if plan != nil {
		d, err := driver.New(plan.Driver, driver.Options{URL: plan.URL, Key: *key, Command: plan.Command})
		if err != nil {
			return err
		}
		ov := driver.Override{PlanID: plan.ID, Driver: plan.Driver, URL: plan.URL, Command: plan.Command, Routes: routes0}
		if err := driver.Remember(*dataDir, ov); err != nil {
			return err
		}
		// Removal must still run after Ctrl-C, so it does not use ctx.
		defer func() {
			rc, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if failed, err := driver.RemoveAll(rc, d, routes0); err != nil {
				fmt.Fprintf(os.Stderr, "有 %d 个测试条目没能删除: %v\n清理: customs probe cleanup\n", len(failed), err)
				return
			}
			_ = driver.Forget(*dataDir, plan.ID)
			fmt.Fprintf(os.Stderr, "已删除 %d 个测试条目\n", len(routes0))
		}()
		var added []string
		for _, rt := range routes0 {
			if err := d.Add(ctx, rt); err != nil {
				fmt.Fprintf(os.Stderr, "跳过 %s: %v\n", rt.Model, err)
				continue
			}
			added = append(added, rt.Key)
		}
		if len(added) == 0 {
			return fmt.Errorf("一个测试条目都没能新增,没有运行任何用例")
		}
		for k, v := range driver.Aliases(routes0) {
			routes[k] = v
		}
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
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	results := r.RunAll(runCtx, cases, sel)
	if *out != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	if *resultsFile != "" {
		type row struct {
			ID           string        `json:"id"`
			Outcome      probe.Outcome `json:"outcome"`
			Detail       string        `json:"detail"`
			UpstreamPath string        `json:"upstream_path,omitempty"`
		}
		rows := make([]row, len(results))
		for i, res := range results {
			d := res.Detail
			if len(d) > 300 {
				d = d[:300]
			}
			rows[i] = row{res.ID, res.Outcome, d, res.UpstreamPath}
		}
		b, _ := json.MarshalIndent(map[string]any{"gateway": *name, "version": *gwVersion, "run_date": time.Now().Format("2006-01-02"), "results": rows}, "", " ")
		if err := os.WriteFile(*resultsFile, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	counts := map[probe.Outcome]int{}
	for _, res := range results {
		counts[res.Outcome]++
	}
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"untrusted": true, "results": results}); err != nil {
			return err
		}
	} else {
		for _, res := range results {
			fmt.Printf("%-12s %-58s %s\n", res.Outcome, res.ID, res.Detail)
		}
		fmt.Printf("\n%d cases: PASS=%d FAIL=%d INCONCLUSIVE=%d SKIP=%d\n", len(results),
			counts[probe.Pass], counts[probe.Fail], counts[probe.Inconclusive], counts[probe.Skip])
	}
	if counts[probe.Fail] > 0 {
		findings = true
	}
	return nil
}

// findings makes the command exit 10 after its deferred cleanup has run.
var findings bool

func cmdProbePlan(args []string) error {
	fs := flag.NewFlagSet("probe plan", flag.ExitOnError)
	drv := fs.String("driver", "", "litellm or magpie")
	url := fs.String("url", "", "base URL of the gateway")
	fakeURL := fs.String("fake-url", "http://127.0.0.1:9100", "the fake upstream as the gateway reaches it (from Docker: http://host.docker.internal:9100)")
	command := fs.String("command", "", `how to run the gateway's CLI, e.g. "docker exec customs-magpie magpie"`)
	dataDir := fs.String("data", defaultDataDir(), "data directory")
	_ = fs.Parse(args)
	if *drv == "" || (*url == "" && *drv == "litellm") {
		return &exitError{2, "usage: customs probe plan --driver litellm|magpie --url GATEWAY_URL [--fake-url URL]"}
	}
	d, err := driver.New(*drv, driver.Options{URL: *url, Command: strings.Fields(*command)})
	if err != nil {
		return &exitError{2, err.Error()}
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	routes := d.Plan(*fakeURL, hex.EncodeToString(b))
	actions := make([]string, len(routes))
	for i, rt := range routes {
		actions[i] = d.Describe(rt)
	}
	rb, _ := json.Marshal(routes)
	c, err := newClient(*dataDir)
	if err != nil {
		return err
	}
	var p api.Plan
	if err := c.do("POST", "/api/plans", api.Plan{Driver: *drv, URL: *url, FakeURL: *fakeURL, Actions: actions, Routes: rb, Command: strings.Fields(*command)}, &p); err != nil {
		return err
	}
	fmt.Printf("计划 %s:对 %s 新增 %d 个临时测试条目,不改已有配置,跑完自动删除。\n", p.ID, *drv, len(actions))
	for _, a := range actions {
		fmt.Println("  " + a)
	}
	fmt.Printf("\n请用户在界面批准: http://%s\n批准后运行: customs probe run --plan %s --key <网关管理员 key>\n", c.d.UI, p.ID)
	return &exitError{3, ""}
}

func cmdProbeCleanup(args []string) error {
	fs := flag.NewFlagSet("probe cleanup", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "data directory")
	key := fs.String("key", os.Getenv("CUSTOMS_GATEWAY_KEY"), "gateway admin key (default: $CUSTOMS_GATEWAY_KEY)")
	_ = fs.Parse(args)
	list, err := driver.LoadManifest(*dataDir)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("没有遗留的测试条目")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, o := range list {
		d, err := driver.New(o.Driver, driver.Options{URL: o.URL, Key: *key, Command: o.Command})
		if err != nil {
			return err
		}
		failed, err := driver.RemoveAll(ctx, d, o.Routes)
		if err != nil && len(failed) == len(o.Routes) {
			return fmt.Errorf("计划 %s 的条目删除失败: %w", o.PlanID, err)
		}
		if err := driver.Forget(*dataDir, o.PlanID); err != nil {
			return err
		}
		fmt.Printf("计划 %s:处理了 %d 个条目,%d 个未能删除(可能本来就没加上)\n", o.PlanID, len(o.Routes), len(failed))
	}
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
