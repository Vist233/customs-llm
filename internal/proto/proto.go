// Package proto names the wire protocols customs understands and recognises them from request paths.
package proto

import "strings"

const (
	Chat      = "openai_chat"
	Responses = "openai_responses"
	Anthropic = "anthropic_messages"
	Gemini    = "gemini_generate"
	Bedrock   = "bedrock_converse"
	Ollama    = "ollama_chat"
	JSONRPC   = "jsonrpc"
)

// OfPath maps a request path (query allowed) to the protocol it speaks, or "".
func OfPath(path string) string {
	p := path
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = strings.ToLower(strings.TrimRight(p, "/"))
	switch {
	case strings.Contains(p, ":generatecontent") || strings.Contains(p, ":streamgeneratecontent"):
		return Gemini
	case strings.HasSuffix(p, "/converse") || strings.HasSuffix(p, "/converse-stream"):
		return Bedrock
	case strings.HasSuffix(p, "/api/chat"):
		return Ollama
	case strings.HasSuffix(p, "/mcp"):
		return JSONRPC
	case strings.HasSuffix(p, "/chat/completions"):
		return Chat
	case strings.HasSuffix(p, "/responses"):
		return Responses
	case strings.HasSuffix(p, "/messages"):
		return Anthropic
	}
	return ""
}
