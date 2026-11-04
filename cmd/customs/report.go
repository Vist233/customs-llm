package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
)

// cmdReport prints a redacted markdown report that can be pasted into an issue.
func cmdReport(args []string) error {
	var pos []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos, args = append(pos, args[0]), args[1:]
	}
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "data directory")
	_ = fs.Bool("md", true, "markdown output (default)")
	_ = fs.Parse(args)
	pos = append(pos, fs.Args()...)
	c, err := newClient(*data)
	if err != nil {
		return err
	}
	id := ""
	if len(pos) == 1 {
		id = pos[0]
	} else {
		var list []map[string]any
		if err := c.get("/api/traces?state=failed&limit=1", &list); err != nil {
			return err
		}
		if len(list) == 0 {
			return fmt.Errorf("no failed trace; pass an ID")
		}
		id = fmt.Sprint(list[0]["id"])
	}
	var d struct {
		Trace map[string]any   `json:"trace"`
		Hops  []map[string]any `json:"hops"`
	}
	if err := c.get("/api/traces/"+id+"?redact=1", &d); err != nil {
		return err
	}
	var rec struct {
		Columns []struct{ Name, Tier string } `json:"columns"`
		Rows    []struct {
			Label string                   `json:"label"`
			Diff  string                   `json:"diff"`
			Cells []struct{ Value string } `json:"cells"`
		} `json:"rows"`
	}
	_ = c.get("/api/traces/"+id+"/reconcile?redact=1", &rec)
	t := d.Trace
	u := t["usage"].(map[string]any)
	var b strings.Builder
	fmt.Fprintf(&b, "## 请求记录 `%s`\n\n> 由 customs-llm %s 生成,密钥已脱敏。证据来源标注在每一列后。\n\n", id, version)
	fmt.Fprintf(&b, "| 项 | 值 |\n|---|---|\n| 请求 | `%v %v` |\n| 模型 | `%v` |\n| 目标 | `%v` |\n| 状态 | %s (HTTP %v) |\n| 耗时 / 首字节 | %s / %s |\n| 用量 | in %s / out %s / cache读 %s |\n| finish | `%v` |\n",
		t["method"], t["path"], t["model"], t["target"], stateLabel[fmt.Sprint(t["state"])], t["status"], dur(t["ms"].(float64)), dur(t["ttfb_ms"].(float64)),
		tok(u["in"].(float64)), tok(u["out"].(float64)), tok(u["cache_read"].(float64)), t["finish"])
	if len(d.Hops) > 0 {
		h := d.Hops[0]
		for _, x := range d.Hops {
			if x["tier"] == "wire" {
				h = x
				break
			}
		}
		if e := fmt.Sprint(h["error"]); e != "" && e != "<nil>" {
			fmt.Fprintf(&b, "\n**传输层错误:** %s\n", e)
		}
		if t["state"] == "http_error" {
			var resp string
			_ = c.get(fmt.Sprintf("/api/traces/%s/hops/%.0f/body?which=resp&redact=1", id, h["id"].(float64)), &resp)
			fmt.Fprintf(&b, "\n### 上游原始响应体(HTTP %v,%d 字节)\n\n```json\n%s\n```\n", h["status"], len(resp), strings.TrimSpace(resp))
		}
	}
	fmt.Fprintf(&b, "\n### 对账(%d 列", len(rec.Columns))
	for _, col := range rec.Columns {
		fmt.Fprintf(&b, ";%s [%s]", col.Name, col.Tier)
	}
	fmt.Fprint(&b, ")\n\n| 项 | 差异 | 值 |\n|---|---|---|\n")
	for _, r := range rec.Rows {
		var cells []string
		for _, cell := range r.Cells {
			v := cell.Value
			if v == "" {
				v = "未知"
			}
			cells = append(cells, "`"+v+"`")
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", r.Label, r.Diff, strings.Join(cells, " → "))
	}
	fmt.Print(b.String())
	_ = json.Marshal
	return nil
}
