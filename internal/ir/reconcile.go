package ir

import (
	"sort"
	"strconv"
	"strings"
)

// RowDef describes one row of the reconcile table.
type RowDef struct {
	Key    string   `json:"key"`
	Group  string   `json:"group"`
	Label  string   `json:"label"`
	Issues []string `json:"issues,omitempty"`
}

// Rows lists the reconcile table rows in display order. Issues point at the test set cases behind each row.
var Rows = []RowDef{
	{"system_messages", "消息与内容", "system 消息数", []string{"portkey#1838", "litellm#48813"}},
	{"developer_messages", "消息与内容", "developer 消息数", []string{"litellm#41913"}},
	{"conversation_messages", "消息与内容", "user / assistant 消息数", []string{"new-api#7920", "new-api#8048"}},
	{"images", "消息与内容", "图片块数", []string{"ccr#1678", "new-api#7578"}},
	{"documents", "消息与内容", "文档/文件块数", []string{"litellm#43737"}},
	{"tools_defined", "工具", "工具定义数", nil},
	{"non_function_tools", "工具", "非 function 类型工具数", []string{"new-api#7192"}},
	{"tool_choice", "工具", "tool_choice", []string{"litellm#39735"}},
	{"parallel_tool_calls", "工具", "parallel_tool_calls", []string{"litellm#41913"}},
	{"schema_features", "工具", "schema 约束(strict/const/anyOf/空 properties…)", []string{"litellm#41913", "litellm#43157", "litellm#24051"}},
	{"tool_calls", "工具", "历史中的工具调用数", []string{"new-api#7920", "magpie#4945"}},
	{"tool_results", "工具", "历史中的工具结果数", []string{"ccr#1643"}},
	{"tool_result_errors", "工具", "其中 is_error 数", []string{"litellm#44979"}},
	{"cache_control", "缓存", "cache_control 标记数", []string{"portkey#1579", "litellm#43324", "magpie#5125"}},
	{"reasoning", "推理", "推理参数", []string{"litellm#49574", "litellm#44560"}},
	{"reasoning_replay", "推理", "回放的推理块数", []string{"litellm#45546", "magpie#4906"}},
	{"max_output", "参数与格式", "输出上限(字段名=值)", []string{"magpie#5030", "new-api#7626"}},
	{"response_format", "参数与格式", "response_format / 结构化输出", []string{"magpie#5082"}},
	{"sampling", "参数与格式", "采样参数", []string{"portkey#1423"}},
	{"stream", "参数与格式", "stream", nil},
	{"metadata", "头与泄漏", "metadata 键", []string{"magpie#4899"}},
	{"added_fields", "头与泄漏", "出站新增的顶层字段", []string{"litellm#46701", "litellm#43582"}},
	{"removed_fields", "头与泄漏", "入站顶层字段在出站消失", []string{"litellm#49479"}},
	{"status", "响应", "状态码", []string{"opencode#51530"}},
	{"error", "响应", "错误", []string{"litellm#45122", "opencode#50352"}},
	{"finish", "响应", "finish / status", []string{"litellm#48721", "litellm#44081"}},
	{"tool_calls_returned", "响应", "返回的工具调用数", nil},
	{"stream_events", "响应", "流事件数", nil},
	{"stream_text", "响应", "流:文本增量", []string{"litellm#45378"}},
	{"stream_reasoning", "响应", "流:推理增量", nil},
	{"stream_tool_call", "响应", "流:工具调用事件", []string{"litellm#45348", "new-api#7252"}},
	{"stream_refusal", "响应", "流:拒绝增量", []string{"litellm#45235"}},
	{"stream_terminal", "响应", "流:收到终止事件", []string{"new-api#7489", "portkey#1832"}},
	{"stream_done_marker", "响应", "流:[DONE]", nil},
	{"stream_usage", "响应", "流:带用量", []string{"ccr#1587"}},
	{"usage_in", "用量", "输入 token", []string{"portkey#858"}},
	{"usage_out", "用量", "输出 token", nil},
	{"usage_cache_read", "用量", "缓存读取", []string{"magpie#5125"}},
	{"usage_cache_write", "用量", "缓存写入", nil},
	{"usage_reasoning", "用量", "推理 token", nil},
}

// Column is one hop's view for the table.
type Column struct {
	Name     string            `json:"name"`
	Tier     string            `json:"tier"`
	Protocol string            `json:"protocol"`
	Facts    Facts             `json:"-"`
	TopKeys  []string          `json:"-"`
	Extra    map[string]string `json:"-"`
}

// Cell is one value in a row.
type Cell struct {
	Value string `json:"value"`
	Tier  string `json:"tier"`
}

// Row is a filled reconcile row.
type Row struct {
	RowDef
	Cells []Cell `json:"cells"`
	// Diff is same, less, more, changed or unknown.
	Diff string `json:"diff"`
}

// Reconcile fills the table for the given columns (in hop order). Rows with no data anywhere are dropped.
func Reconcile(cols []Column) []Row {
	facts := make([]Facts, len(cols))
	for i, c := range cols {
		f := Facts{}
		for k, v := range c.Facts {
			f[k] = v
		}
		facts[i] = f
	}
	// top-level field changes need two request columns of the same protocol
	var first = -1
	for i, c := range cols {
		if c.TopKeys == nil {
			continue
		}
		if first < 0 {
			first = i
			continue
		}
		facts[first]["added_fields"], facts[first]["removed_fields"] = "-", "-"
		if cols[first].Protocol == c.Protocol {
			add, rem := diffKeys(cols[first].TopKeys, c.TopKeys)
			facts[i]["added_fields"], facts[i]["removed_fields"] = listOrDash(add), listOrDash(rem)
		} else {
			facts[i]["added_fields"], facts[i]["removed_fields"] = "(协议不同,不比较)", "(协议不同,不比较)"
		}
	}
	var out []Row
	for _, def := range Rows {
		row := Row{RowDef: def, Cells: make([]Cell, len(cols))}
		known := 0
		for i, c := range cols {
			v, ok := facts[i][def.Key]
			if def.Key == "added_fields" || def.Key == "removed_fields" {
				if !ok {
					v = ""
				}
			}
			row.Cells[i] = Cell{Value: v, Tier: c.Tier}
			if v != "" {
				known++
			}
		}
		if known == 0 {
			continue
		}
		row.Diff = diffOf(def.Key, row.Cells)
		out = append(out, row)
	}
	return out
}

func listOrDash(ks []string) string {
	if len(ks) == 0 {
		return "-"
	}
	return strings.Join(ks, ", ")
}

func diffKeys(a, b []string) (added, removed []string) {
	in := func(s []string, k string) bool {
		for _, x := range s {
			if x == k {
				return true
			}
		}
		return false
	}
	for _, k := range b {
		if !in(a, k) {
			added = append(added, k)
		}
	}
	for _, k := range a {
		if !in(b, k) {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}

func diffOf(key string, cells []Cell) string {
	var vals []string
	for _, c := range cells {
		if c.Value != "" {
			vals = append(vals, c.Value)
		}
	}
	if len(vals) < 2 {
		return "unknown"
	}
	if key == "added_fields" {
		for _, v := range vals {
			if v != "-" && !strings.HasPrefix(v, "(") {
				return "more"
			}
		}
		return "same"
	}
	if key == "removed_fields" {
		for _, v := range vals {
			if v != "-" && !strings.HasPrefix(v, "(") {
				return "less"
			}
		}
		return "same"
	}
	firstV, lastV := vals[0], vals[len(vals)-1]
	allSame := true
	for _, v := range vals {
		if v != firstV {
			allSame = false
		}
	}
	if allSame {
		return "same"
	}
	a, errA := strconv.Atoi(firstV)
	b, errB := strconv.Atoi(lastV)
	if errA == nil && errB == nil {
		if b < a {
			return "less"
		}
		return "more"
	}
	return "changed"
}

// UsageFacts converts usage into row facts; unknown values are omitted.
func UsageFacts(f Facts, in, out, cr, cw, rs int) {
	put := func(k string, n int) {
		if n >= 0 {
			f[k] = strconv.Itoa(n)
		}
	}
	put("usage_in", in)
	put("usage_out", out)
	put("usage_cache_read", cr)
	put("usage_cache_write", cw)
	put("usage_reasoning", rs)
}
