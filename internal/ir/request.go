// Package ir reads requests and responses of the supported protocols into protocol-neutral facts,
// so that hops speaking different protocols can be compared row by row.
package ir

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Vist233/customs-llm/internal/proto"
)

// Facts maps a row key (see Rows) to its value as text. A missing key means unknown.
type Facts map[string]string

type obj = map[string]any

func asObj(v any) obj   { o, _ := v.(obj); return o }
func asArr(v any) []any { a, _ := v.([]any); return a }
func str(v any) string  { s, _ := v.(string); return s }

func walk(v any, fn func(o obj)) {
	switch x := v.(type) {
	case obj:
		fn(x)
		for _, e := range x {
			walk(e, fn)
		}
	case []any:
		for _, e := range x {
			walk(e, fn)
		}
	}
}

func countKey(v any, key string) int {
	n := 0
	walk(v, func(o obj) {
		if _, ok := o[key]; ok {
			n++
		}
	})
	return n
}

func countTypes(v any, types ...string) int {
	set := map[string]bool{}
	for _, t := range types {
		set[t] = true
	}
	n := 0
	walk(v, func(o obj) {
		if set[str(o["type"])] {
			n++
		}
	})
	return n
}

func schemaFeatures(schema any) map[string]int {
	f := map[string]int{}
	walk(schema, func(o obj) {
		for _, k := range []string{"const", "enum", "anyOf", "oneOf", "allOf", "$ref", "additionalProperties"} {
			if _, ok := o[k]; ok {
				f[k]++
			}
		}
		if p, ok := o["properties"].(obj); ok && len(p) == 0 {
			f["emptyProperties"]++
		}
	})
	return f
}

func itoa(n int) string { return strconv.Itoa(n) }

func keysOf(o obj) []string {
	out := make([]string, 0, len(o))
	for k := range o {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Request reads a request body of the given protocol. top lists its top-level keys.
func Request(protocol string, body []byte) (facts Facts, top []string, err error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, nil, err
	}
	o := asObj(v)
	if o == nil {
		return nil, nil, fmt.Errorf("request body is not a JSON object")
	}
	f := Facts{}
	top = keysOf(o)
	f["cache_control"] = itoa(countKey(o, "cache_control"))
	if s, ok := o["stream"].(bool); ok {
		f["stream"] = strconv.FormatBool(s)
	} else {
		f["stream"] = "false"
	}
	f["metadata"] = metaKeys(o)
	f["sampling"] = sampling(o)
	switch protocol {
	case proto.Chat:
		chatRequest(o, f)
	case proto.Responses:
		responsesRequest(o, f)
	case proto.Anthropic:
		anthropicRequest(o, f)
	default:
		return f, top, nil
	}
	return f, top, nil
}

func metaKeys(o obj) string {
	var ks []string
	if m := asObj(o["metadata"]); m != nil {
		ks = append(ks, keysOf(m)...)
	}
	if _, ok := o["user"]; ok {
		ks = append(ks, "user")
	}
	if len(ks) == 0 {
		return "-"
	}
	return strings.Join(ks, ",")
}

func sampling(o obj) string {
	var parts []string
	for _, k := range []string{"temperature", "top_p", "top_k", "frequency_penalty", "presence_penalty", "seed", "stop", "stop_sequences", "n"} {
		if v, ok := o[k]; ok {
			b, _ := json.Marshal(v)
			parts = append(parts, k+"="+string(b))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func schemaSummary(tools []any, schemaOf func(t obj) any, strictOf func(t obj) bool) string {
	total := map[string]int{}
	for _, t := range tools {
		to := asObj(t)
		for k, n := range schemaFeatures(schemaOf(to)) {
			total[k] += n
		}
		if strictOf(to) {
			total["strict"]++
		}
	}
	if len(total) == 0 {
		return "-"
	}
	ks := make([]string, 0, len(total))
	for k := range total {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = k + "=" + itoa(total[k])
	}
	return strings.Join(parts, " ")
}

func normChoice(v any) string {
	switch c := v.(type) {
	case nil:
		return "-"
	case string:
		if c == "any" {
			return "required"
		}
		return c
	case obj:
		t := str(c["type"])
		if fn := asObj(c["function"]); fn != nil {
			return "named:" + str(fn["name"])
		}
		if n := str(c["name"]); n != "" {
			return "named:" + n
		}
		if t == "any" {
			return "required"
		}
		if t != "" {
			return t
		}
	}
	return "-"
}

func boolText(v any) string {
	if b, ok := v.(bool); ok {
		return strconv.FormatBool(b)
	}
	return "-"
}

func chatRequest(o obj, f Facts) {
	msgs := asArr(o["messages"])
	var sys, dev, conv, calls, results, replay int
	for _, m := range msgs {
		mo := asObj(m)
		switch str(mo["role"]) {
		case "system":
			sys++
		case "developer":
			dev++
		case "tool":
			results++
		case "user", "assistant":
			conv++
		}
		calls += len(asArr(mo["tool_calls"]))
		if _, ok := mo["reasoning_content"]; ok {
			replay++
		} else if _, ok := mo["reasoning"]; ok {
			replay++
		}
	}
	f["system_messages"], f["developer_messages"], f["conversation_messages"] = itoa(sys), itoa(dev), itoa(conv)
	f["images"] = itoa(countTypes(msgs, "image_url", "image", "input_image"))
	f["documents"] = itoa(countTypes(msgs, "file", "document", "input_file"))
	f["tool_calls"], f["tool_results"], f["tool_result_errors"], f["reasoning_replay"] = itoa(calls), itoa(results), "-", itoa(replay)
	tools := asArr(o["tools"])
	f["tools_defined"] = itoa(len(tools))
	non := 0
	for _, t := range tools {
		if str(asObj(t)["type"]) != "function" {
			non++
		}
	}
	f["non_function_tools"] = itoa(non)
	f["schema_features"] = schemaSummary(tools, func(t obj) any { return asObj(t["function"])["parameters"] },
		func(t obj) bool { b, _ := asObj(t["function"])["strict"].(bool); return b })
	f["tool_choice"] = normChoice(o["tool_choice"])
	f["parallel_tool_calls"] = boolText(o["parallel_tool_calls"])
	f["reasoning"] = firstNonEmpty(str(o["reasoning_effort"]), str(asObj(o["reasoning"])["effort"]))
	f["max_output"] = maxOutput(o, "max_tokens", "max_completion_tokens")
	f["response_format"] = firstNonEmpty(str(asObj(o["response_format"])["type"]))
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return "-"
}

func maxOutput(o obj, keys ...string) string {
	for _, k := range keys {
		if v, ok := o[k]; ok {
			b, _ := json.Marshal(v)
			return k + "=" + string(b)
		}
	}
	return "-"
}

func responsesRequest(o obj, f Facts) {
	var sys, dev, conv, calls, results, replay int
	if str(o["instructions"]) != "" {
		sys++
	}
	input := o["input"]
	if _, ok := input.(string); ok {
		conv++
	}
	for _, it := range asArr(input) {
		io := asObj(it)
		switch str(io["type"]) {
		case "function_call":
			calls++
		case "function_call_output":
			results++
		case "reasoning":
			replay++
		case "message", "":
			switch str(io["role"]) {
			case "system":
				sys++
			case "developer":
				dev++
			case "user", "assistant":
				conv++
			}
			// function calls nested in message content are still calls
			for _, part := range asArr(io["content"]) {
				if str(asObj(part)["type"]) == "function_call" {
					calls++
				}
			}
		}
	}
	f["system_messages"], f["developer_messages"], f["conversation_messages"] = itoa(sys), itoa(dev), itoa(conv)
	f["images"] = itoa(countTypes(input, "image_url", "image", "input_image"))
	f["documents"] = itoa(countTypes(input, "file", "document", "input_file"))
	f["tool_calls"], f["tool_results"], f["tool_result_errors"], f["reasoning_replay"] = itoa(calls), itoa(results), "-", itoa(replay)
	tools := asArr(o["tools"])
	f["tools_defined"] = itoa(len(tools))
	non := 0
	for _, t := range tools {
		if str(asObj(t)["type"]) != "function" {
			non++
		}
	}
	f["non_function_tools"] = itoa(non)
	f["schema_features"] = schemaSummary(tools, func(t obj) any { return t["parameters"] },
		func(t obj) bool { b, _ := t["strict"].(bool); return b })
	f["tool_choice"] = normChoice(o["tool_choice"])
	f["parallel_tool_calls"] = boolText(o["parallel_tool_calls"])
	f["reasoning"] = firstNonEmpty(str(asObj(o["reasoning"])["effort"]))
	f["max_output"] = maxOutput(o, "max_output_tokens")
	f["response_format"] = firstNonEmpty(str(asObj(asObj(o["text"])["format"])["type"]))
}

func anthropicRequest(o obj, f Facts) {
	sys := 0
	switch s := o["system"].(type) {
	case string:
		if s != "" {
			sys = 1
		}
	case []any:
		sys = len(s)
	}
	msgs := asArr(o["messages"])
	var conv, calls, results, errs, replay int
	for _, m := range msgs {
		mo := asObj(m)
		onlyResults := false
		if blocks, ok := mo["content"].([]any); ok && len(blocks) > 0 {
			onlyResults = true
			for _, b := range blocks {
				bo := asObj(b)
				switch str(bo["type"]) {
				case "tool_result":
					results++
					if e, _ := bo["is_error"].(bool); e {
						errs++
					}
				case "tool_use":
					calls++
					onlyResults = false
				case "thinking", "redacted_thinking":
					replay++
					onlyResults = false
				default:
					onlyResults = false
				}
			}
		}
		if !onlyResults {
			conv++
		}
	}
	f["system_messages"], f["developer_messages"], f["conversation_messages"] = itoa(sys), "0", itoa(conv)
	f["images"] = itoa(countTypes(msgs, "image", "image_url", "input_image"))
	f["documents"] = itoa(countTypes(msgs, "document", "file", "input_file"))
	f["tool_calls"], f["tool_results"], f["tool_result_errors"], f["reasoning_replay"] = itoa(calls), itoa(results), itoa(errs), itoa(replay)
	tools := asArr(o["tools"])
	f["tools_defined"] = itoa(len(tools))
	non := 0
	for _, t := range tools {
		if tt := str(asObj(t)["type"]); tt != "" && tt != "custom" {
			non++
		}
	}
	f["non_function_tools"] = itoa(non)
	f["schema_features"] = schemaSummary(tools, func(t obj) any { return t["input_schema"] },
		func(t obj) bool { b, _ := t["strict"].(bool); return b })
	f["tool_choice"] = normChoice(o["tool_choice"])
	par := "-"
	if d, ok := asObj(o["tool_choice"])["disable_parallel_tool_use"].(bool); ok {
		par = strconv.FormatBool(!d)
	}
	f["parallel_tool_calls"] = par
	reasoning := "-"
	if th := asObj(o["thinking"]); th != nil {
		reasoning = str(th["type"])
		if b, ok := th["budget_tokens"]; ok {
			reasoning += fmt.Sprintf(" budget=%v", b)
		}
	}
	if e := str(asObj(o["output_config"])["effort"]); e != "" {
		if reasoning == "-" {
			reasoning = ""
		}
		reasoning = strings.TrimSpace(reasoning + " effort=" + e)
	}
	f["reasoning"] = reasoning
	f["max_output"] = maxOutput(o, "max_tokens")
	f["response_format"] = firstNonEmpty(str(asObj(asObj(o["output_config"])["format"])["type"]), str(asObj(o["output_format"])["type"]))
}
