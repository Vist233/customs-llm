// Package redact masks secrets before data leaves the local store (CLI output, reports, exports).
// The store itself keeps raw bytes; redaction happens on the way out.
package redact

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	// sensitiveKey matches header and JSON key names whose values are secrets.
	sensitiveKey = regexp.MustCompile(`(?i)(authorization|api[-_]?key|apikey|token|secret|password|passwd|credential|cookie|signature|private[-_]?key|x-amz-security)`)
	// tokenLike matches secrets embedded in free text.
	tokenLike = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=\-]{8,}`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{12,}`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`),
		regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{30,}`),
	}
)

// Mask hides the middle of a secret and keeps a short prefix so different keys stay tellable apart.
func Mask(s string) string {
	if s == "" {
		return s
	}
	prefix := ""
	if i := strings.IndexAny(s, " "); i > 0 && i < 8 { // "Bearer xxx"
		prefix, s = s[:i+1], s[i+1:]
	}
	keep := 4
	if len(s) <= 8 {
		keep = 0
	}
	return prefix + s[:keep] + "…[redacted]"
}

// Text masks token-like substrings in free text.
func Text(s string) string {
	for _, re := range tokenLike {
		s = re.ReplaceAllStringFunc(s, Mask)
	}
	return s
}

// Headers returns a copy of h with secret values masked.
func Headers(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if sensitiveKey.MatchString(k) {
			out[k] = Mask(v)
		} else {
			out[k] = Text(v)
		}
	}
	return out
}

// Value walks decoded JSON and masks secrets by key name and by shape.
func Value(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if s, ok := e.(string); ok && sensitiveKey.MatchString(k) && !isPlainWord(k) {
				out[k] = Mask(s)
				continue
			}
			out[k] = Value(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Value(e)
		}
		return out
	case string:
		return Text(x)
	}
	return v
}

// isPlainWord keeps keys such as "max_tokens" or "input_tokens" readable: they hold counts, not secrets.
func isPlainWord(k string) bool {
	l := strings.ToLower(k)
	return strings.HasSuffix(l, "_tokens") || l == "tokens" || strings.HasPrefix(l, "max_") || strings.Contains(l, "token_count") ||
		strings.HasPrefix(l, "tool_") || strings.HasPrefix(l, "total_tokens")
}

// JSON redacts a JSON document; input that is not JSON is redacted as text.
func JSON(b []byte) []byte {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return []byte(Text(string(b)))
	}
	out, err := json.Marshal(Value(v))
	if err != nil {
		return []byte(Text(string(b)))
	}
	return out
}
