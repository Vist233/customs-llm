package collect

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Vist233/customs-llm/internal/model"
	"github.com/Vist233/customs-llm/internal/store"
)

// OpenCode reads finished assistant messages from opencode's session database. opencode's own log file stays
// empty unless logs are printed, so this table is the durable record of failures. Prompts are not imported.
type OpenCode struct {
	Path     string
	Store    *store.Store
	OnChange func(string)
	status   Status
}

// DefaultOpenCodeDB follows opencode's own rule: $XDG_DATA_HOME, else ~/.local/share, on every platform.
func DefaultOpenCodeDB() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "opencode", "opencode.db")
}

func (o *OpenCode) Name() string   { return "opencode-db" }
func (o *OpenCode) State() *Status { return &o.status }

type ocMessage struct {
	Role       string `json:"role"`
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Finish     string `json:"finish"`
	Time       struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	Tokens struct {
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Error *struct {
		Name string `json:"name"`
		Data struct {
			Message      string `json:"message"`
			StatusCode   int    `json:"statusCode"`
			ResponseBody string `json:"responseBody"`
		} `json:"data"`
	} `json:"error"`
}

// Poll imports messages updated since the last poll.
func (o *OpenCode) Poll(ctx context.Context) (int, error) {
	if _, err := os.Stat(o.Path); err != nil {
		return 0, fmt.Errorf("没有找到 %s", o.Path)
	}
	db, err := sql.Open("sqlite", store.URI(o.Path, "mode=ro&_pragma=busy_timeout(2000)"))
	if err != nil {
		return 0, err
	}
	defer db.Close()
	cur, _ := o.Store.GetKV(ctx, "cursor:opencode")
	since, _ := strconv.ParseInt(cur, 10, 64)
	rows, err := db.QueryContext(ctx, `SELECT id,session_id,time_updated,data FROM message WHERE time_updated>? ORDER BY time_updated LIMIT 2000`, since)
	if err != nil {
		return 0, fmt.Errorf("读取 opencode 数据库: %w", err)
	}
	type row struct {
		id, session string
		updated     int64
		data        string
	}
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.session, &r.updated, &r.data); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, r)
	}
	rows.Close()
	n := 0
	for _, r := range batch {
		since = r.updated
		var m ocMessage
		if json.Unmarshal([]byte(r.data), &m) != nil || m.Role != "assistant" || (m.Time.Completed == 0 && m.Error == nil) {
			continue
		}
		key := "opencode:" + r.id
		if id, _ := o.Store.TraceByKey(ctx, key); id != "" {
			continue
		}
		h := openCodeHop(r.id, r.session, r.updated, &m)
		if err := o.Store.SaveHop(ctx, h); err != nil {
			return n, err
		}
		_ = o.Store.Link(ctx, key, h.TraceID)
		if o.OnChange != nil {
			o.OnChange(h.TraceID)
		}
		n++
	}
	return n, o.Store.SetKV(ctx, "cursor:opencode", strconv.FormatInt(since, 10))
}

func openCodeHop(id, session string, updated int64, m *ocMessage) *model.Hop {
	end := m.Time.Completed
	if end == 0 {
		end = updated
	}
	h := &model.Hop{TraceID: "oc-" + id, Source: "opencode", Tier: model.TierReported, From: "opencode", To: m.ProviderID,
		Started: time.UnixMilli(m.Time.Created), Ended: time.UnixMilli(end), Model: m.ModelID, Finish: m.Finish, Method: "LOG", URI: "opencode session " + session,
		Usage: model.Usage{In: m.Tokens.Input, Out: m.Tokens.Output, CacheRead: m.Tokens.Cache.Read, CacheWrite: m.Tokens.Cache.Write, Reasoning: m.Tokens.Reasoning},
		Note:  map[string]string{"session": session, "message": id}}
	if e := m.Error; e != nil {
		h.Status = e.Data.StatusCode
		if h.Status == 0 {
			h.Error = e.Name + ": " + e.Data.Message
		}
		h.Note["error_name"], h.Note["error_message"] = e.Name, e.Data.Message
		h.RespBody = []byte(e.Data.ResponseBody)
		h.Usage = model.UnknownUsage()
	}
	return h
}
