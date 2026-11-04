package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Vist233/customs-llm/internal/model"
)

// Filter narrows ListTraces.
type Filter struct {
	State  string // running, ok, http_error, transport_error, incomplete; "failed" = any non-ok non-running
	Query  string // substring of model, path, client or target
	Since  time.Time
	Limit  int
	Offset int
}

// TraceState derives a state from a hop.
func TraceState(h *model.Hop) string {
	switch {
	case h.Ended.IsZero():
		return "running"
	case h.Error != "":
		return "transport_error"
	case h.Status >= 400:
		return "http_error"
	case h.Streaming && h.Finish == "":
		return "incomplete"
	}
	return "ok"
}

// RefreshTrace recomputes a trace's summary row from its hops.
func (s *Store) RefreshTrace(ctx context.Context, id string) error {
	hops, err := s.Hops(ctx, id)
	if err != nil || len(hops) == 0 {
		return err
	}
	p := &hops[0]
	for i := range hops {
		if hops[i].Tier == model.TierWire {
			p = &hops[i]
			break
		}
	}
	target := p.To
	client := p.From
	ms, ttfb := int64(0), int64(0)
	if !p.Ended.IsZero() {
		ms = p.Ended.Sub(p.Started).Milliseconds()
	}
	if !p.FirstByte.IsZero() {
		ttfb = p.FirstByte.Sub(p.Started).Milliseconds()
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO traces(id,started_us,client,target,protocol,model,method,path,status,state,streaming,ms,ttfb_ms,usage,finish,hops,flags)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET client=excluded.client,target=excluded.target,protocol=excluded.protocol,model=excluded.model,method=excluded.method,
		path=excluded.path,status=excluded.status,state=excluded.state,streaming=excluded.streaming,ms=excluded.ms,ttfb_ms=excluded.ttfb_ms,
		usage=excluded.usage,finish=excluded.finish,hops=excluded.hops`,
		id, us(p.Started), client, target, p.Protocol, p.Model, p.Method, p.URI, p.Status, TraceState(p), boolInt(p.Streaming), ms, ttfb,
		jsonStr(p.Usage), p.Finish, len(hops), "[]")
	return err
}

// SetFlags stores check flags on a trace.
func (s *Store) SetFlags(ctx context.Context, id string, flags []string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE traces SET flags=? WHERE id=?`, jsonStr(flags), id)
	return err
}

func scanSummary(sc interface{ Scan(...any) error }) (model.TraceSummary, error) {
	var t model.TraceSummary
	var started int64
	var streaming int
	var usage, flags string
	err := sc.Scan(&t.ID, &started, &t.Client, &t.Target, &t.Protocol, &t.Model, &t.Method, &t.Path, &t.Status, &t.State, &streaming,
		&t.Millis, &t.TTFBMillis, &usage, &t.Finish, &t.Hops, &flags)
	t.Started = fromUS(started)
	t.Streaming = streaming == 1
	_ = json.Unmarshal([]byte(usage), &t.Usage)
	_ = json.Unmarshal([]byte(flags), &t.Flags)
	return t, err
}

const summaryCols = `id,started_us,client,target,protocol,model,method,path,status,state,streaming,ms,ttfb_ms,usage,finish,hops,flags`

// ListTraces returns summaries, newest first.
func (s *Store) ListTraces(ctx context.Context, f Filter) ([]model.TraceSummary, error) {
	var where []string
	var args []any
	switch f.State {
	case "":
	case "failed":
		where = append(where, "state NOT IN ('ok','running')")
	default:
		where = append(where, "state=?")
		args = append(args, f.State)
	}
	if f.Query != "" {
		where = append(where, "(model LIKE ? OR path LIKE ? OR client LIKE ? OR target LIKE ?)")
		q := "%" + f.Query + "%"
		args = append(args, q, q, q, q)
	}
	if !f.Since.IsZero() {
		where = append(where, "started_us>=?")
		args = append(args, f.Since.UnixMicro())
	}
	q := "SELECT " + summaryCols + " FROM traces"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	q += " ORDER BY started_us DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TraceSummary
	for rows.Next() {
		t, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Trace returns one summary.
func (s *Store) Trace(ctx context.Context, id string) (*model.TraceSummary, error) {
	t, err := scanSummary(s.db.QueryRowContext(ctx, "SELECT "+summaryCols+" FROM traces WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &t, err
}

const hopCols = `id,trace_id,seq,source,tier,from_,to_,started_us,first_byte_us,ended_us,protocol,method,uri,req_header,req_blob,status,resp_header,resp_blob,streaming,model,finish,usage,error,note,events`

// Hops returns a trace's hops with bodies loaded.
func (s *Store) Hops(ctx context.Context, traceID string) ([]model.Hop, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+hopCols+" FROM hops WHERE trace_id=? ORDER BY seq,id", traceID)
	if err != nil {
		return nil, err
	}
	var out []model.Hop
	var reqBlobs, respBlobs []string
	for rows.Next() {
		var h model.Hop
		var st, fb, en int64
		var streaming int
		var reqH, respH, usage, note, rb, pb string
		if err := rows.Scan(&h.ID, &h.TraceID, &h.Seq, &h.Source, &h.Tier, &h.From, &h.To, &st, &fb, &en, &h.Protocol, &h.Method, &h.URI,
			&reqH, &rb, &h.Status, &respH, &pb, &streaming, &h.Model, &h.Finish, &usage, &h.Error, &note, &h.Events); err != nil {
			rows.Close()
			return nil, err
		}
		h.Started, h.FirstByte, h.Ended = fromUS(st), fromUS(fb), fromUS(en)
		h.Streaming = streaming == 1
		_ = json.Unmarshal([]byte(reqH), &h.ReqHeader)
		_ = json.Unmarshal([]byte(respH), &h.RespHdr)
		_ = json.Unmarshal([]byte(usage), &h.Usage)
		_ = json.Unmarshal([]byte(note), &h.Note)
		out = append(out, h)
		reqBlobs, respBlobs = append(reqBlobs, rb), append(respBlobs, pb)
	}
	rows.Close()
	for i := range out {
		out[i].ReqBody, _ = s.Blob(reqBlobs[i])
		out[i].RespBody, _ = s.Blob(respBlobs[i])
	}
	return out, nil
}

// Events returns a hop's stream events with data loaded.
func (s *Store) Events(ctx context.Context, hopID int64) ([]model.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT idx,offset_us,kind,name,data_blob FROM events WHERE hop_id=? ORDER BY idx`, hopID)
	if err != nil {
		return nil, err
	}
	var out []model.Event
	var blobs []string
	for rows.Next() {
		e := model.Event{HopID: hopID}
		var b string
		if err := rows.Scan(&e.Idx, &e.OffsetUS, &e.Kind, &e.Name, &b); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
		blobs = append(blobs, b)
	}
	rows.Close()
	for i := range out {
		out[i].Data, _ = s.Blob(blobs[i])
	}
	return out, nil
}
