package ir

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/proto"
)

// Response is what could be read from a response.
type Response struct {
	Model  string
	Finish string
	Usage  model.Usage
	Facts  Facts
}

func num(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return -1
}

func decode(b []byte) any {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return v
}

// ReadResponse reads a response of the given protocol. For streams pass the events; for other responses the body.
func ReadResponse(protocol string, status int, body []byte, events []model.Event) Response {
	r := Response{Usage: model.UnknownUsage(), Facts: Facts{"status": strconv.Itoa(status)}}
	if len(events) > 0 {
		readStream(protocol, events, &r)
	} else if v := decode(body); v != nil {
		readBody(protocol, v, &r)
	}
	return r
}

func setUsage(r *Response, in, out, cr, cw, rs any) {
	if n := num(in); n >= 0 {
		r.Usage.In = n
	}
	if n := num(out); n >= 0 {
		r.Usage.Out = n
	}
	if n := num(cr); n >= 0 {
		r.Usage.CacheRead = n
	}
	if n := num(cw); n >= 0 {
		r.Usage.CacheWrite = n
	}
	if n := num(rs); n >= 0 {
		r.Usage.Reasoning = n
	}
}

func readBody(protocol string, v any, r *Response) {
	o := asObj(v)
	if o == nil {
		return
	}
	r.Model = str(o["model"])
	if e := asObj(o["error"]); e != nil {
		r.Facts["error"] = firstNonEmpty(str(e["code"]), str(e["type"])) + ": " + str(e["message"])
	}
	switch protocol {
	case proto.Chat:
		if ch := asArr(o["choices"]); len(ch) > 0 {
			c := asObj(ch[0])
			r.Finish = str(c["finish_reason"])
			r.Facts["tool_calls_returned"] = itoa(len(asArr(asObj(c["message"])["tool_calls"])))
		}
		if u := asObj(o["usage"]); u != nil {
			setUsage(r, u["prompt_tokens"], u["completion_tokens"], asObj(u["prompt_tokens_details"])["cached_tokens"], nil, asObj(u["completion_tokens_details"])["reasoning_tokens"])
		}
	case proto.Responses:
		r.Finish = str(o["status"])
		if inc := asObj(o["incomplete_details"]); inc != nil {
			r.Finish += ":" + str(inc["reason"])
		}
		n := 0
		for _, it := range asArr(o["output"]) {
			if str(asObj(it)["type"]) == "function_call" {
				n++
			}
		}
		r.Facts["tool_calls_returned"] = itoa(n)
		if u := asObj(o["usage"]); u != nil {
			setUsage(r, u["input_tokens"], u["output_tokens"], asObj(u["input_tokens_details"])["cached_tokens"], nil, asObj(u["output_tokens_details"])["reasoning_tokens"])
		}
	case proto.Anthropic:
		r.Finish = str(o["stop_reason"])
		n := 0
		for _, b := range asArr(o["content"]) {
			if str(asObj(b)["type"]) == "tool_use" {
				n++
			}
		}
		r.Facts["tool_calls_returned"] = itoa(n)
		if u := asObj(o["usage"]); u != nil {
			setUsage(r, u["input_tokens"], u["output_tokens"], u["cache_read_input_tokens"], u["cache_creation_input_tokens"], nil)
		}
	case proto.Gemini:
		if c := asArr(o["candidates"]); len(c) > 0 {
			r.Finish = str(asObj(c[0])["finishReason"])
		}
		if u := asObj(o["usageMetadata"]); u != nil {
			setUsage(r, u["promptTokenCount"], u["candidatesTokenCount"], u["cachedContentTokenCount"], nil, u["thoughtsTokenCount"])
		}
	}
}

func readStream(protocol string, events []model.Event, r *Response) {
	counts := map[string]int{}
	terminal, done, usageEvent := false, false, false
	for _, e := range events {
		if e.Kind != "data" {
			continue
		}
		data := strings.TrimSpace(string(e.Data))
		if data == "[DONE]" {
			done = true
			continue
		}
		o := asObj(decode(e.Data))
		if o == nil {
			continue
		}
		if m := str(o["model"]); m != "" && r.Model == "" {
			r.Model = m
		}
		if er := asObj(o["error"]); er != nil {
			r.Facts["error"] = firstNonEmpty(str(er["code"]), str(er["type"])) + ": " + str(er["message"])
		}
		switch protocol {
		case proto.Chat:
			for _, c := range asArr(o["choices"]) {
				co := asObj(c)
				d := asObj(co["delta"])
				if str(d["content"]) != "" {
					counts["text"]++
				}
				if firstNonEmpty(str(d["reasoning_content"]), str(d["reasoning"])) != "-" {
					counts["reasoning"]++
				}
				if len(asArr(d["tool_calls"])) > 0 {
					counts["tool_call"]++
				}
				if str(d["refusal"]) != "" {
					counts["refusal"]++
				}
				if f := str(co["finish_reason"]); f != "" {
					r.Finish, terminal = f, true
				}
			}
			if u := asObj(o["usage"]); u != nil {
				usageEvent = true
				setUsage(r, u["prompt_tokens"], u["completion_tokens"], asObj(u["prompt_tokens_details"])["cached_tokens"], nil, asObj(u["completion_tokens_details"])["reasoning_tokens"])
			}
		case proto.Responses:
			t := str(o["type"])
			switch {
			case t == "response.output_text.delta":
				counts["text"]++
			case strings.HasPrefix(t, "response.reasoning") && strings.HasSuffix(t, ".delta"):
				counts["reasoning"]++
			case t == "response.function_call_arguments.delta" || t == "response.function_call_arguments.done":
				counts["tool_call"]++
			case strings.HasPrefix(t, "response.refusal") && strings.HasSuffix(t, ".delta"):
				counts["refusal"]++
			case t == "response.completed" || t == "response.incomplete" || t == "response.failed":
				terminal = true
				resp := asObj(o["response"])
				r.Finish = str(resp["status"])
				if inc := asObj(resp["incomplete_details"]); inc != nil {
					r.Finish += ":" + str(inc["reason"])
				}
				if u := asObj(resp["usage"]); u != nil {
					usageEvent = true
					setUsage(r, u["input_tokens"], u["output_tokens"], asObj(u["input_tokens_details"])["cached_tokens"], nil, asObj(u["output_tokens_details"])["reasoning_tokens"])
				}
			case t == "error":
				r.Facts["error"] = firstNonEmpty(str(o["code"])) + ": " + str(o["message"])
			}
		case proto.Anthropic:
			t := str(o["type"])
			switch t {
			case "message_start":
				if u := asObj(asObj(o["message"])["usage"]); u != nil {
					setUsage(r, u["input_tokens"], u["output_tokens"], u["cache_read_input_tokens"], u["cache_creation_input_tokens"], nil)
				}
			case "content_block_start":
				if str(asObj(o["content_block"])["type"]) == "tool_use" {
					counts["tool_call"]++
				}
			case "content_block_delta":
				switch str(asObj(o["delta"])["type"]) {
				case "text_delta":
					counts["text"]++
				case "thinking_delta":
					counts["reasoning"]++
				case "input_json_delta":
					counts["tool_call_args"]++
				}
			case "message_delta":
				if s := str(asObj(o["delta"])["stop_reason"]); s != "" {
					r.Finish = s
				}
				if u := asObj(o["usage"]); u != nil {
					usageEvent = true
					if n := num(u["output_tokens"]); n >= 0 {
						r.Usage.Out = n
					}
					if n := num(u["input_tokens"]); n >= 0 {
						r.Usage.In = n
					}
				}
			case "message_stop":
				terminal = true
			}
		}
	}
	r.Facts["stream_events"] = itoa(len(events))
	r.Facts["stream_text"], r.Facts["stream_reasoning"] = itoa(counts["text"]), itoa(counts["reasoning"])
	r.Facts["stream_tool_call"], r.Facts["stream_refusal"] = itoa(counts["tool_call"]), itoa(counts["refusal"])
	r.Facts["stream_terminal"] = yesNo(terminal)
	r.Facts["stream_usage"] = yesNo(usageEvent)
	if protocol == proto.Chat {
		r.Facts["stream_done_marker"] = yesNo(done)
	}
	if protocol == proto.Anthropic && counts["tool_call"] > 0 {
		r.Facts["stream_tool_args"] = itoa(counts["tool_call_args"])
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
