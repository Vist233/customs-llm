package ir

import (
	"encoding/json"
	"testing"

	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/proto"
	"github.com/Vist233/customs-llm/internal/testset"
)

func TestChatRequestFacts(t *testing.T) {
	body := `{"model":"m","stream":true,"reasoning_effort":"low","max_tokens":9,"temperature":0,
	 "tool_choice":{"type":"function","function":{"name":"get_weather"}},
	 "messages":[{"role":"system","content":"s"},{"role":"developer","content":"d"},{"role":"user","content":[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"u"}}],"cache_control":{"type":"ephemeral"}},
	  {"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"r"}],
	 "tools":[{"type":"function","function":{"name":"f","strict":true,"parameters":{"type":"object","properties":{}}}}]}`
	f, top, err := Request(proto.Chat, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := Facts{"system_messages": "1", "developer_messages": "1", "conversation_messages": "2", "images": "1", "tool_calls": "1", "tool_results": "1",
		"tools_defined": "1", "tool_choice": "named:get_weather", "reasoning": "low", "max_output": "max_tokens=9", "cache_control": "1", "stream": "true",
		"sampling": "temperature=0", "schema_features": "emptyProperties=1 strict=1"}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
	if len(top) == 0 || top[0] != "max_tokens" {
		t.Errorf("top keys: %v", top)
	}
}

func TestAnthropicRequestFacts(t *testing.T) {
	body := `{"model":"m","max_tokens":5,"system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}],
	 "thinking":{"type":"enabled","budget_tokens":1024},
	 "messages":[{"role":"user","content":"go"},
	  {"role":"assistant","content":[{"type":"text","text":"t"},{"type":"tool_use","id":"a","name":"n","input":{}},{"type":"tool_use","id":"b","name":"n","input":{}}]},
	  {"role":"user","content":[{"type":"tool_result","tool_use_id":"a","is_error":true,"content":"x"},{"type":"tool_result","tool_use_id":"b","content":"y"}]}]}`
	f, _, err := Request(proto.Anthropic, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range (Facts{"system_messages": "1", "conversation_messages": "2", "tool_calls": "2", "tool_results": "2", "tool_result_errors": "1",
		"cache_control": "1", "reasoning": "enabled budget=1024", "max_output": "max_tokens=5"}) {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
}

func TestResponsesRequestFacts(t *testing.T) {
	body := `{"model":"m","instructions":"i","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"x"},{"type":"input_image","image_url":"u"}]},
	 {"type":"function_call","call_id":"c","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"c","output":"o"},{"type":"reasoning","id":"r"}],
	 "tools":[{"type":"web_search"}],"reasoning":{"effort":"high"},"max_output_tokens":7}`
	f, _, err := Request(proto.Responses, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range (Facts{"system_messages": "1", "conversation_messages": "1", "images": "1", "tool_calls": "1", "tool_results": "1", "reasoning_replay": "1",
		"non_function_tools": "1", "reasoning": "high", "max_output": "max_output_tokens=7"}) {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
}

func ev(s string) model.Event { return model.Event{Kind: "data", Data: []byte(s)} }

func TestReadResponses(t *testing.T) {
	r := ReadResponse(proto.Chat, 200, []byte(`{"model":"m","choices":[{"finish_reason":"length","message":{"tool_calls":[{}]}}],"usage":{"prompt_tokens":5,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":2}}}`), nil)
	if r.Finish != "length" || r.Usage.In != 5 || r.Usage.Out != 7 || r.Usage.CacheRead != 2 || r.Usage.Reasoning != -1 || r.Facts["tool_calls_returned"] != "1" {
		t.Errorf("chat body: %+v", r)
	}
	r = ReadResponse(proto.Chat, 200, nil, []model.Event{ev(`{"choices":[{"delta":{"content":"a"}}]}`), ev(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`), ev(`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2}}`), ev("[DONE]")})
	if r.Finish != "stop" || r.Usage.Out != 2 || r.Facts["stream_text"] != "1" || r.Facts["stream_terminal"] != "yes" || r.Facts["stream_done_marker"] != "yes" || r.Facts["stream_usage"] != "yes" {
		t.Errorf("chat stream: %+v", r)
	}
	r = ReadResponse(proto.Anthropic, 200, nil, []model.Event{ev(`{"type":"message_start","message":{"usage":{"input_tokens":4,"output_tokens":0}}}`),
		ev(`{"type":"content_block_start","content_block":{"type":"tool_use"}}`), ev(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`), ev(`{"type":"message_stop"}`)})
	if r.Finish != "tool_use" || r.Usage.In != 4 || r.Usage.Out != 9 || r.Facts["stream_tool_call"] != "1" || r.Facts["stream_tool_args"] != "0" {
		t.Errorf("anthropic stream: %+v", r)
	}
	r = ReadResponse(proto.Responses, 200, nil, []model.Event{ev(`{"type":"response.output_text.delta"}`), ev(`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":2}}}`)})
	if r.Finish != "completed" || r.Usage.Out != 2 || r.Facts["stream_terminal"] != "yes" {
		t.Errorf("responses stream: %+v", r)
	}
}

func TestReconcile(t *testing.T) {
	a, ta, _ := Request(proto.Chat, []byte(`{"model":"m","messages":[{"role":"user","content":"x"}],"tools":[],"reasoning_effort":"low","id":1}`))
	b, tb, _ := Request(proto.Chat, []byte(`{"model":"m","messages":[{"role":"user","content":"x"}],"tools":[],"azure_ad_token":"t"}`))
	rows := Reconcile([]Column{{Name: "client", Tier: "wire", Protocol: proto.Chat, Facts: a, TopKeys: ta}, {Name: "upstream", Tier: "wire", Protocol: proto.Chat, Facts: b, TopKeys: tb}})
	byKey := map[string]Row{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	if byKey["reasoning"].Diff != "changed" {
		t.Errorf("reasoning diff: %+v", byKey["reasoning"])
	}
	if r := byKey["added_fields"]; r.Cells[1].Value != "azure_ad_token" || r.Diff != "more" {
		t.Errorf("added_fields: %+v", r)
	}
	if r := byKey["removed_fields"]; r.Cells[1].Value != "id, reasoning_effort" || r.Diff != "less" {
		t.Errorf("removed_fields: %+v", r)
	}
	if byKey["conversation_messages"].Diff != "same" {
		t.Errorf("messages: %+v", byKey["conversation_messages"])
	}
}

// Every test set request body must be readable by the parser of its client protocol.
func TestParsesAllTestsetRequests(t *testing.T) {
	cases, err := testset.Load("../../testset/cases")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		p := proto.OfPath(c.Inbound.Path)
		if p != proto.Chat && p != proto.Responses && p != proto.Anthropic {
			continue
		}
		if _, _, err := Request(p, c.Inbound.Body); err != nil {
			t.Errorf("%s: %v", c.ID, err)
		}
		var v any
		_ = json.Unmarshal(c.Inbound.Body, &v)
	}
}
