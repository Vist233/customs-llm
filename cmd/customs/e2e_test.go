package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Vist233/customs-llm/internal/fakeup"
)

// TestEndToEnd builds the real binary and drives it the way a user or an agent does. It runs on every CI platform.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "customs")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	data := filepath.Join(dir, "data")
	run := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "CUSTOMS_DATA="+data)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return string(out), code
	}
	if out, code := run("traces", "list"); code != 4 {
		t.Fatalf("no daemon: exit %d, want 4: %s", code, out)
	}
	if out, code := run("testset", "validate"); code != 0 || !strings.HasPrefix(out, "ok:") {
		t.Fatalf("testset: %d %s", code, out)
	}

	up := exec.Command(bin, "up", "--relay-listen", "127.0.0.1:0", "--ui-listen", "127.0.0.1:0", "--opencode-db=")
	up.Env = append(os.Environ(), "CUSTOMS_DATA="+data)
	var upLog bytes.Buffer
	up.Stdout, up.Stderr = &upLog, &upLog
	if err := up.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = up.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			_ = up.Process.Kill()
			<-done
		}
	}()
	var d daemonFile
	for i := 0; ; i++ {
		if b, err := os.ReadFile(filepath.Join(data, "daemon.json")); err == nil && json.Unmarshal(b, &d) == nil {
			break
		}
		if i > 100 {
			t.Fatalf("daemon did not start: %s", upLog.String())
		}
		time.Sleep(100 * time.Millisecond)
	}

	fake := httptest.NewServer(fakeup.New().Handler())
	defer fake.Close()
	if out, code := run("target", "add", "fake", fake.URL+"/v1"); code != 0 {
		t.Fatalf("target add: %d %s", code, out)
	}
	req, _ := http.NewRequest("POST", "http://"+d.Relay+"/fake/chat/completions", strings.NewReader(`{"model":"e2e","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-secretsecretsecretsecret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("relay: %d", resp.StatusCode)
	}

	var id string
	for i := 0; i < 50 && id == ""; i++ {
		out, _ := run("traces", "list", "--json")
		var l struct {
			Traces []struct{ ID, State string } `json:"traces"`
		}
		if json.Unmarshal([]byte(out), &l) == nil && len(l.Traces) == 1 && l.Traces[0].State == "ok" {
			id = l.Traces[0].ID
		}
		time.Sleep(50 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("trace never appeared")
	}
	raw, code := run("traces", "show", id, "--section", "raw")
	if code != 0 || !strings.Contains(raw, "redacted") || strings.Contains(raw, "secretsecret") {
		t.Fatalf("redaction: %d %s", code, raw)
	}
	if rep, code := run("report", id); code != 0 || !strings.Contains(rep, "e2e") || strings.Contains(rep, "secretsecret") {
		t.Fatalf("report: %d %s", code, rep)
	}
	if out, code := run("probe", "plan", "--driver", "litellm", "--url", "http://127.0.0.1:4000"); code != 3 {
		t.Fatalf("plan must wait for approval: %d %s", code, out)
	}
	page, err := http.Get("http://" + d.UI + "/")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(page.Body)
	page.Body.Close()
	if strings.Contains(buf.String(), "__CUSTOMS") || !strings.Contains(buf.String(), "app.js") {
		t.Fatalf("page: %s", buf.String())
	}
	if out, code := run("up", "--relay-listen", "127.0.0.1:0", "--ui-listen", "127.0.0.1:0", "--opencode-db="); code != 0 || !strings.Contains(out, d.UI) {
		t.Fatalf("second up should report the running one: %d %s", code, out)
	}
	if out, code := run("down"); code != 0 {
		t.Fatalf("down: %d %s", code, out)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not exit")
	}
	if _, err := os.Stat(filepath.Join(data, "daemon.json")); !os.IsNotExist(err) {
		t.Fatalf("daemon.json left behind: %v", err)
	}
}
