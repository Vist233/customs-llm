package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/proto"
	"github.com/Vist233/customs-llm/internal/store"
)

// LiteLLM reads the proxy's spend logs (needs a database and an admin key). Bodies appear only when the proxy
// stores prompts; the outbound request LiteLLM built is not in these logs at all.
type LiteLLM struct {
	URL      string
	Key      string
	Store    *store.Store
	OnChange func(string)
	HTTP     *http.Client
	// Window is how far back each poll looks; rows are written late, in batches.
	Window time.Duration
	status Status
}

func (l *LiteLLM) Name() string   { return "litellm-spend-logs" }
func (l *LiteLLM) State() *Status { return &l.status }

type spendRow struct {
	RequestID  string          `json:"request_id"`
	CallID     string          `json:"litellm_call_id"`
	CallType   string          `json:"call_type"`
	Model      string          `json:"model"`
	ModelGroup string          `json:"model_group"`
	Provider   string          `json:"custom_llm_provider"`
	APIBase    string          `json:"api_base"`
	Status     string          `json:"status"`
	Start      string          `json:"startTime"`
	First      string          `json:"completionStartTime"`
	End        string          `json:"endTime"`
	Prompt     int             `json:"prompt_tokens"`
	Completion int             `json:"completion_tokens"`
	Request    json.RawMessage `json:"proxy_server_request"`
	Response   json.RawMessage `json:"response"`
}

// Poll imports rows that are not yet known.
func (l *LiteLLM) Poll(ctx context.Context) (int, error) {
	win := l.Window
	if win == 0 {
		win = 24 * time.Hour
	}
	now := time.Now().UTC()
	n := 0
	for page := 1; page <= 50; page++ {
		q := url.Values{"start_date": {now.Add(-win).Format("2006-01-02 15:04:05")}, "end_date": {now.Add(time.Hour).Format("2006-01-02 15:04:05")},
			"page": {fmt.Sprint(page)}, "page_size": {"100"}}
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(l.URL, "/")+"/spend/logs/v2?"+q.Encode(), nil)
		if err != nil {
			return n, err
		}
		req.Header.Set("Authorization", "Bearer "+l.Key)
		c := l.HTTP
		if c == nil {
			c = &http.Client{Timeout: 30 * time.Second}
		}
		resp, err := c.Do(req)
		if err != nil {
			return n, err
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return n, fmt.Errorf("spend logs → %d: %.200s", resp.StatusCode, b)
		}
		var d struct {
			Data       []spendRow `json:"data"`
			TotalPages int        `json:"total_pages"`
		}
		if err := json.Unmarshal(b, &d); err != nil {
			return n, err
		}
		for i := range d.Data {
			added, err := l.importRow(ctx, &d.Data[i])
			if err != nil {
				return n, err
			}
			if added {
				n++
			}
		}
		if page >= d.TotalPages {
			break
		}
	}
	return n, nil
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func hasContent(b json.RawMessage) bool {
	t := string(bytes.TrimSpace(b))
	return t != "" && t != "null" && t != "{}"
}

func (l *LiteLLM) importRow(ctx context.Context, r *spendRow) (bool, error) {
	id := r.RequestID
	if id == "" {
		id = r.CallID
	}
	key := "litellm-row:" + id
	if t, _ := l.Store.TraceByKey(ctx, key); t != "" {
		return false, nil
	}
	h := &model.Hop{Source: "litellm", Tier: model.TierReported, From: r.ModelGroup, To: r.Provider, Started: parseTime(r.Start), FirstByte: parseTime(r.First),
		Ended: parseTime(r.End), Model: r.Model, Method: "LOG", URI: r.APIBase, Note: map[string]string{"status": r.Status, "call_type": r.CallType, "request_id": r.RequestID},
		Usage: model.Usage{In: r.Prompt, Out: r.Completion, CacheRead: -1, CacheWrite: -1, Reasoning: -1}}
	if h.Ended.IsZero() {
		h.Ended = h.Started
	}
	if r.Status == "failure" {
		h.Error = "LiteLLM 记录此次调用失败"
		h.Usage = model.UnknownUsage()
	}
	switch r.CallType {
	case "acompletion", "completion":
		h.Protocol = proto.Chat
	case "aresponses", "responses":
		h.Protocol = proto.Responses
	case "anthropic_messages":
		h.Protocol = proto.Anthropic
	}
	if hasContent(r.Request) {
		h.ReqBody = r.Request
	}
	if hasContent(r.Response) {
		h.RespBody = r.Response
	}
	// The relay links the response's x-litellm-call-id, which joins this row to the trace it saw on the wire.
	trace, _ := l.Store.TraceByKey(ctx, "litellm:"+r.CallID)
	if trace != "" {
		h.TraceID, h.Seq = trace, 1
	} else {
		h.TraceID = "ll-" + id
	}
	if err := l.Store.SaveHop(ctx, h); err != nil {
		return false, err
	}
	if err := l.Store.Link(ctx, key, h.TraceID); err != nil {
		return false, err
	}
	if l.OnChange != nil {
		l.OnChange(h.TraceID)
	}
	return true, nil
}
