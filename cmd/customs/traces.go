package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var stateLabel = map[string]string{"ok": "成功", "http_error": "HTTP错误", "transport_error": "连接失败", "incomplete": "流未结束", "running": "进行中"}

func tok(n float64) string {
	if n < 0 {
		return "-"
	}
	return strconv.Itoa(int(n))
}

func dur(ms float64) string {
	if ms <= 0 {
		return "-"
	}
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", ms/1000)
	}
	return fmt.Sprintf("%.0fms", ms)
}

func cmdTraces(args []string) error {
	if len(args) == 0 {
		return &exitError{2, "usage: customs traces list|show"}
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("traces "+sub, flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "data directory")
	asJSON := fs.Bool("json", false, "JSON output")
	raw := fs.Bool("raw", false, "do not redact secrets (avoid: the output goes into your context)")
	failed := fs.Bool("failed", false, "only failed requests")
	running := fs.Bool("running", false, "only running requests")
	since := fs.String("since", "", "only the last DURATION, e.g. 1h")
	query := fs.String("q", "", "substring of model, path, client or target")
	limit := fs.Int("limit", 20, "max rows")
	section := fs.String("section", "summary", "summary|reconcile|error|stream|chat|raw")
	all := fs.Bool("all", false, "show every reconcile row, including identical ones")
	// allow the id before the flags: customs traces show ID --section error
	var pos []string
	for len(rest) > 0 {
		if strings.HasPrefix(rest[0], "-") {
			break
		}
		pos, rest = append(pos, rest[0]), rest[1:]
	}
	_ = fs.Parse(rest)
	pos = append(pos, fs.Args()...)
	c, err := newClient(*data)
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		st := ""
		if *failed {
			st = "failed"
		} else if *running {
			st = "running"
		}
		var list []map[string]any
		if err := c.get("/api/traces?"+q(map[string]string{"state": st, "since": *since, "q": *query, "limit": strconv.Itoa(*limit)}), &list); err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"untrusted": true, "traces": list})
		}
		if len(list) == 0 {
			fmt.Println("没有记录")
			return nil
		}
		fmt.Printf("%-16s %-8s %-9s %-34s %-12s %9s %-14s %s\n", "ID", "时间", "状态", "请求", "目标", "耗时", "in/out/cache", "标记")
		for _, t := range list {
			started, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(t["started"]))
			u := t["usage"].(map[string]any)
			var flags []string
			if fl, ok := t["flags"].([]any); ok {
				for _, x := range fl {
					flags = append(flags, fmt.Sprint(x))
				}
			}
			fmt.Printf("%-16v %-8s %-9s %-34s %-12v %9s %-14s %s\n", t["id"], started.Local().Format("15:04:05"), stateLabel[fmt.Sprint(t["state"])],
				trunc(fmt.Sprint(t["method"])+" "+fmt.Sprint(t["path"]), 34), t["target"], dur(t["ms"].(float64)), tok(u["in"].(float64))+"/"+tok(u["out"].(float64))+"/"+tok(u["cache_read"].(float64)), strings.Join(flags, " "))
		}
		return nil
	case "show":
		if len(pos) != 1 {
			return &exitError{2, "usage: customs traces show ID [--section ...]"}
		}
		return showTrace(c, pos[0], *section, *asJSON, *raw, *all)
	}
	return &exitError{2, "unknown traces command " + sub}
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func showTrace(c *client, id, section string, asJSON, raw, all bool) error {
	red := redactParam(raw)
	var data struct {
		Trace map[string]any   `json:"trace"`
		Hops  []map[string]any `json:"hops"`
	}
	if err := c.get("/api/traces/"+id+"?"+red, &data); err != nil {
		return err
	}
	t := data.Trace
	var hop map[string]any
	for _, h := range data.Hops {
		if h["tier"] == "wire" {
			hop = h
			break
		}
	}
	if hop == nil && len(data.Hops) > 0 {
		hop = data.Hops[0]
	}
	hopID := fmt.Sprintf("%.0f", hop["id"].(float64))
	switch section {
	case "summary":
		var rec struct {
			Rows []struct {
				Label string                         `json:"label"`
				Group string                         `json:"group"`
				Diff  string                         `json:"diff"`
				Cells []struct{ Value, Tier string } `json:"cells"`
			} `json:"rows"`
			Columns []struct{ Name, Tier string } `json:"columns"`
		}
		_ = c.get("/api/traces/"+id+"/reconcile?"+red, &rec)
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"untrusted": true, "trace": t, "hops": data.Hops, "reconcile": rec})
		}
		u := t["usage"].(map[string]any)
		fmt.Printf("%s\n请求   %v %v  (%v → %v)\n模型   %v\n状态   %s  HTTP %v\n耗时   %s  首字节 %s\n用量   in %s / out %s / cache读 %s  finish=%v\n",
			untrustedNote, t["method"], t["path"], t["client"], t["target"], t["model"], stateLabel[fmt.Sprint(t["state"])], t["status"],
			dur(t["ms"].(float64)), dur(t["ttfb_ms"].(float64)), tok(u["in"].(float64)), tok(u["out"].(float64)), tok(u["cache_read"].(float64)), t["finish"])
		if e := fmt.Sprint(hop["error"]); e != "" && e != "<nil>" {
			fmt.Printf("错误   %s\n", e)
		}
		if t["state"] == "http_error" {
			fmt.Printf("提示   上游返回了错误体,用 --section error 看原始内容\n")
		}
		fmt.Printf("\n对账(%d 列:", len(rec.Columns))
		for i, col := range rec.Columns {
			if i > 0 {
				fmt.Print(" | ")
			}
			fmt.Printf("%s[%s]", col.Name, col.Tier)
		}
		fmt.Println(")")
		shown, hidden := 0, 0
		for _, r := range rec.Rows {
			if !all && len(rec.Columns) > 1 && r.Diff == "same" {
				hidden++
				continue
			}
			var cells []string
			for _, cell := range r.Cells {
				v := cell.Value
				if v == "" {
					v = "未知"
				}
				cells = append(cells, v)
			}
			if len(rec.Columns) < 2 {
				fmt.Printf("  %-34s %s\n", r.Label, strings.Join(cells, " | "))
			} else {
				fmt.Printf("  %-34s %-8s %s\n", r.Label, r.Diff, strings.Join(cells, "  →  "))
			}
			shown++
		}
		if hidden > 0 {
			fmt.Printf("  (另有 %d 项一致,--all 显示)\n", hidden)
		}
		if len(rec.Columns) < 2 {
			fmt.Println("  只有一跳记录,无法对比;见 customs guide 的探测一节。")
		}
		return nil
	case "reconcile":
		var out string
		if err := c.get("/api/traces/"+id+"/reconcile?"+red, &out); err != nil {
			return err
		}
		fmt.Println(out)
	case "error", "raw", "chat":
		var req, resp string
		_ = c.get("/api/traces/"+id+"/hops/"+hopID+"/body?which=req&"+red, &req)
		_ = c.get("/api/traces/"+id+"/hops/"+hopID+"/body?which=resp&"+red, &resp)
		if section == "error" {
			fmt.Printf("%s\n状态码 %v  传输层错误: %v\n响应体 %d 字节\n---\n%s\n", untrustedNote, hop["status"], orNone(hop["error"]), len(resp), resp)
			return nil
		}
		fmt.Printf("%s\n--- 请求头\n%v\n--- 请求体\n%s\n--- 响应头\n%v\n--- 响应体\n%s\n", untrustedNote, hop["req_header"], req, hop["resp_header"], resp)
	case "stream":
		var out string
		if err := c.get("/api/traces/"+id+"/hops/"+hopID+"/events?"+red, &out); err != nil {
			return err
		}
		var evs []map[string]any
		_ = json.Unmarshal([]byte(out), &evs)
		fmt.Println(untrustedNote)
		prev := 0.0
		for _, e := range evs {
			t := e["offset_us"].(float64) / 1000
			fmt.Printf("%4.0f  +%-9s 间隔 %-8s %-8v %s\n", e["idx"], dur(t), dur(t-prev), e["kind"], trunc(fmt.Sprint(e["data"]), 100))
			prev = t
		}
	default:
		return &exitError{2, "unknown section " + section}
	}
	return nil
}

func orNone(v any) any {
	if s := fmt.Sprint(v); s == "" || s == "<nil>" {
		return "无"
	}
	return v
}
