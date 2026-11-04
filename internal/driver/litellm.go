package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LiteLLM uses the runtime model API (/model/new, /model/delete), which needs a proxy with a database
// and STORE_MODEL_IN_DB enabled, and a key allowed to manage models.
type LiteLLM struct {
	URL  string
	Key  string
	HTTP *http.Client
}

func (l *LiteLLM) Name() string { return "litellm" }

func (l *LiteLLM) Plan(fakeURL, suffix string) []Route {
	fake := strings.TrimRight(fakeURL, "/")
	v1 := fake + "/v1"
	mk := func(key, short, proto string, params map[string]any, also ...string) Route {
		name := "customs-probe-" + suffix + "-" + short
		if _, aws := params["aws_access_key_id"]; !aws {
			params["api_key"] = "customs-fake"
		}
		return Route{Key: key, Model: name, Ref: name, Proto: proto, Also: also, Spec: map[string]any{"litellm_params": params}}
	}
	return []Route{
		mk("*->openai_chat", "chat", "openai_chat", map[string]any{"model": "openai/customs-chat", "api_base": v1}),
		// openai/ models bridge Responses and Messages clients to /v1/responses unless told to use chat.
		mk("openai_responses->openai_chat", "chat-bridge", "openai_chat", map[string]any{"model": "openai/customs-chat", "api_base": v1, "use_chat_completions_api": true}, "anthropic_messages->openai_chat"),
		mk("route:mistral", "mistral", "openai_chat", map[string]any{"model": "mistral/customs-chat", "api_base": v1}),
		mk("*->openai_responses", "responses", "openai_responses", map[string]any{"model": "openai/customs-responses", "api_base": v1}),
		mk("openai_chat->openai_responses", "responses-prefix", "openai_responses", map[string]any{"model": "openai/responses/customs-responses", "api_base": v1}),
		mk("*->anthropic_messages", "anthropic", "anthropic_messages", map[string]any{"model": "anthropic/customs-anthropic", "api_base": fake}),
		mk("*->gemini_generate", "gemini", "gemini_generate", map[string]any{"model": "gemini/customs-gemini", "api_base": fake + "/v1beta"}, "route:gemini", "route:google", "route:vertex"),
		mk("*->bedrock_converse", "bedrock", "bedrock_converse", map[string]any{"model": "bedrock/converse/anthropic.customs-bedrock", "aws_bedrock_runtime_endpoint": fake,
			"aws_access_key_id": "customs-fake", "aws_secret_access_key": "customs-fake", "aws_region_name": "us-east-1"}, "route:bedrock"),
		mk("*->ollama_chat", "ollama", "ollama_chat", map[string]any{"model": "ollama_chat/customs-ollama", "api_base": fake}, "route:ollama"),
	}
}

func (l *LiteLLM) Describe(r Route) string {
	p, _ := r.Spec["litellm_params"].(map[string]any)
	base := p["api_base"]
	if base == nil {
		base = p["aws_bedrock_runtime_endpoint"]
	}
	return fmt.Sprintf("POST %s/model/new  %s → %v @ %v", strings.TrimRight(l.URL, "/"), r.Model, p["model"], base)
}

func (l *LiteLLM) post(ctx context.Context, path string, body any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(l.URL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.Key)
	c := l.HTTP
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 300 {
		// The reply may echo encrypted params; only the status and a short message are useful.
		msg := string(out)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%s → %d: %s", path, resp.StatusCode, msg)
	}
	return nil
}

func (l *LiteLLM) Add(ctx context.Context, r Route) error {
	params := map[string]any{}
	if p, ok := r.Spec["litellm_params"].(map[string]any); ok {
		params = p
	}
	return l.post(ctx, "/model/new", map[string]any{"model_name": r.Model, "litellm_params": params,
		"model_info": map[string]any{"id": r.Ref, "supports_reasoning": true}})
}

func (l *LiteLLM) Remove(ctx context.Context, r Route) error {
	return l.post(ctx, "/model/delete", map[string]any{"id": r.Ref})
}
