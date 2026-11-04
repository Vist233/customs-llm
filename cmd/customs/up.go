package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Vist233/customs-llm/internal/api"
	"github.com/Vist233/customs-llm/internal/collect"
	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/relay"
	"github.com/Vist233/customs-llm/internal/store"
	"github.com/Vist233/customs-llm/internal/testset"
	testsetdata "github.com/Vist233/customs-llm/testset"
)

//go:embed guide.md
var guideText string

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func defaultDataDir() string {
	if v := os.Getenv("CUSTOMS_DATA"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".customs-llm"
	}
	return filepath.Join(home, ".customs-llm")
}

// daemonFile is how the command line finds a running daemon.
type daemonFile struct {
	PID   int    `json:"pid"`
	UI    string `json:"ui"`
	Relay string `json:"relay"`
	Token string `json:"token"`
	Data  string `json:"data"`
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

type tsProvider struct {
	cases   []testset.Case
	results []json.RawMessage
}

func (p *tsProvider) Cases() any {
	type view struct {
		ID            string              `json:"id"`
		Title         string              `json:"title"`
		Source        testset.Source      `json:"source"`
		Direction     string              `json:"direction"`
		Protocol      testset.Protocol    `json:"protocol"`
		Severity      string              `json:"severity"`
		Category      string              `json:"category"`
		Observed      string              `json:"observed"`
		Notes         string              `json:"notes,omitempty"`
		PayloadOrigin string              `json:"payload_origin"`
		Verified      bool                `json:"verified"`
		Assertions    []testset.Assertion `json:"assertions"`
	}
	out := make([]view, len(p.cases))
	for i, c := range p.cases {
		out[i] = view{c.ID, c.Title, c.Source, c.Direction, c.Protocol, c.Severity, c.Category, c.Observed, c.Notes, c.PayloadOrigin, c.Verified, c.Assertions}
	}
	return out
}
func (p *tsProvider) Results() any { return p.results }

func loadProvider() (*tsProvider, error) {
	cases, err := loadTestset("")
	if err != nil {
		return nil, err
	}
	p := &tsProvider{cases: cases}
	entries, _ := testsetdata.Results.ReadDir("results")
	for _, e := range entries {
		if b, err := testsetdata.Results.ReadFile("results/" + e.Name()); err == nil && json.Valid(b) {
			p.results = append(p.results, json.RawMessage(b))
		}
	}
	return p, nil
}

func cmdUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "data directory")
	relayAddr := fs.String("relay-listen", "127.0.0.1:9000", "address of the recording relay")
	uiAddr := fs.String("ui-listen", "127.0.0.1:7070", "address of the web UI and API")
	litellmURL := fs.String("litellm-url", "", "read this LiteLLM's spend logs (needs its database and an admin key)")
	litellmKey := fs.String("litellm-key", os.Getenv("CUSTOMS_LITELLM_KEY"), "admin key for --litellm-url (default: $CUSTOMS_LITELLM_KEY)")
	opencodeDB := fs.String("opencode-db", collect.DefaultOpenCodeDB(), "opencode session database to read; empty turns it off")
	openBrowser := fs.Bool("open", false, "open the web UI in the default browser")
	var targets multi
	fs.Var(&targets, "target", "relay target NAME=UPSTREAM_URL (repeatable)")
	_ = fs.Parse(args)

	for _, a := range []string{*relayAddr, *uiAddr} {
		host, _, err := net.SplitHostPort(a)
		if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1" && host != "[::1]") {
			return fmt.Errorf("%s: customs only listens on loopback addresses (records contain secrets)", a)
		}
	}
	if err := os.MkdirAll(*data, 0o700); err != nil {
		return err
	}
	// A second launch (a double-click, say) just shows the one already running.
	if c, err := newClient(*data); err == nil && c.get("/api/status", nil) == nil {
		fmt.Printf("customs 已在运行: http://%s\n", c.d.UI)
		if *openBrowser {
			openURL("http://" + c.d.UI)
		}
		return nil
	}
	st, err := store.Open(*data)
	if err != nil {
		return err
	}
	defer st.Close()

	rl := relay.New(st)
	hub := api.NewHub()
	rl.OnChange = hub.Publish
	saved := loadTargets(*data)
	for n, u := range saved {
		_ = rl.SetTarget(n, u)
	}
	for _, t := range targets {
		n, u, ok := strings.Cut(t, "=")
		if !ok {
			return fmt.Errorf("bad --target %q, want NAME=URL", t)
		}
		if err := rl.SetTarget(n, u); err != nil {
			return err
		}
	}
	saveTargets(*data, rl)

	var collectors []collect.Collector
	if *opencodeDB != "" {
		collectors = append(collectors, &collect.OpenCode{Path: *opencodeDB, Store: st, OnChange: hub.Publish})
	}
	if *litellmURL != "" {
		collectors = append(collectors, &collect.LiteLLM{URL: *litellmURL, Key: *litellmKey, Store: st, OnChange: hub.Publish})
	}

	tokBytes := make([]byte, 16)
	_, _ = rand.Read(tokBytes)
	token := hex.EncodeToString(tokBytes)
	ts, err := loadProvider()
	if err != nil {
		return err
	}
	binary, _ := os.Executable()
	startedAt := time.Now().Format(time.RFC3339)
	srv := &api.Server{Store: st, Relay: rl, Hub: hub, Token: token, Testset: ts, Guide: guideText,
		Info: func() api.Info {
			return api.Info{Version: version, DataDir: *data, RelayListen: *relayAddr, UIListen: *uiAddr, Binary: binary, PID: os.Getpid(), Platform: runtime.GOOS + "/" + runtime.GOARCH,
				Started: startedAt, DaemonFile: filepath.Join(*data, "daemon.json"), Sources: sources(rl, collectors)}
		},
		OnTargets: func() { saveTargets(*data, rl) }}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.Shutdown = stop

	relayLn, err := net.Listen("tcp", *relayAddr)
	if err != nil {
		return err
	}
	uiLn, err := net.Listen("tcp", *uiAddr)
	if err != nil {
		return err
	}
	relaySrv := &http.Server{Handler: rl}
	uiSrv := &http.Server{Handler: srv.Handler()}
	go func() { _ = relaySrv.Serve(relayLn) }()
	go func() { _ = uiSrv.Serve(uiLn) }()

	df := daemonFile{PID: os.Getpid(), UI: uiLn.Addr().String(), Relay: relayLn.Addr().String(), Token: token, Data: *data}
	b, _ := json.Marshal(df)
	dfPath := filepath.Join(*data, "daemon.json")
	if err := os.WriteFile(dfPath, b, 0o600); err != nil {
		return err
	}
	defer func() {
		// Only remove our own file: a newer daemon may already have replaced it.
		if cur, err := os.ReadFile(dfPath); err == nil && string(cur) == string(b) {
			_ = os.Remove(dfPath)
		}
	}()
	fmt.Printf("customs-llm %s\n  界面   http://%s\n  中转   http://%s/<目标名>/...\n  数据   %s\n", version, df.UI, df.Relay, *data)
	if len(rl.Targets()) == 0 {
		fmt.Println("  目标   (还没有)  添加: customs target add NAME http://127.0.0.1:4000")
	}
	for _, t := range rl.Targets() {
		fmt.Printf("  目标   %s → %s\n", t.Name, t.Upstream)
	}

	if *openBrowser {
		openURL("http://" + df.UI)
	}
	for _, c := range collectors {
		go collect.Run(ctx, c, 10*time.Second, "每 10 秒读取一次")
	}
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = relaySrv.Shutdown(sctx)
	_ = uiSrv.Shutdown(sctx)
	return nil
}

func openURL(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "无法打开浏览器,请手动访问", u)
	}
}

func sources(rl *relay.Relay, collectors []collect.Collector) []api.Source {
	n := len(rl.Targets())
	detail := fmt.Sprintf("%d 个目标", n)
	if n == 0 {
		detail = "还没有目标;添加后让客户端经过它"
	}
	out := []api.Source{{Name: "relay", Kind: "relay", Tier: model.TierWire, Status: "running", Detail: detail}}
	seen := map[string]bool{}
	for _, c := range collectors {
		state, d, imported := c.State().Get()
		if state == "" {
			state, d = "running", "启动中"
		}
		if state == "running" {
			d = fmt.Sprintf("%s,已导入 %d 条", d, imported)
		}
		seen[c.Name()] = true
		out = append(out, api.Source{Name: c.Name(), Kind: "collector", Tier: model.TierReported, Status: state, Detail: d})
	}
	if !seen["litellm-spend-logs"] {
		out = append(out, api.Source{Name: "litellm-spend-logs", Kind: "collector", Tier: model.TierReported, Status: "idle", Detail: "未配置;用 --litellm-url 和 --litellm-key 开启"})
	}
	if !seen["opencode-db"] {
		out = append(out, api.Source{Name: "opencode-db", Kind: "collector", Tier: model.TierReported, Status: "idle", Detail: "未开启"})
	}
	return out
}

func targetsPath(dir string) string { return filepath.Join(dir, "targets.json") }

func loadTargets(dir string) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(targetsPath(dir)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func saveTargets(dir string, rl *relay.Relay) {
	m := map[string]string{}
	for _, t := range rl.Targets() {
		m[t.Name] = t.Upstream
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(targetsPath(dir), b, 0o600)
}
