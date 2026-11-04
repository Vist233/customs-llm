// Package store keeps traces in SQLite and bodies in content-addressed gzip files.
// Raw data is the truth; summaries are recomputed from it.
package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Vist233/customs-llm/internal/model"
)

type Store struct {
	db  *sql.DB
	dir string
	Now func() time.Time
}

const schemaVersion = 1

const schema = `
CREATE TABLE traces(
  id TEXT PRIMARY KEY, started_us INTEGER NOT NULL, client TEXT, target TEXT, protocol TEXT, model TEXT,
  method TEXT, path TEXT, status INTEGER, state TEXT, streaming INTEGER, ms INTEGER, ttfb_ms INTEGER,
  usage TEXT, finish TEXT, hops INTEGER, flags TEXT);
CREATE INDEX traces_started ON traces(started_us);
CREATE INDEX traces_state ON traces(state);
CREATE TABLE hops(
  id INTEGER PRIMARY KEY AUTOINCREMENT, trace_id TEXT NOT NULL, seq INTEGER, source TEXT, tier TEXT,
  from_ TEXT, to_ TEXT, started_us INTEGER, first_byte_us INTEGER, ended_us INTEGER, protocol TEXT,
  method TEXT, uri TEXT, req_header TEXT, req_blob TEXT, status INTEGER, resp_header TEXT, resp_blob TEXT,
  streaming INTEGER, model TEXT, finish TEXT, usage TEXT, error TEXT, note TEXT, events INTEGER);
CREATE INDEX hops_trace ON hops(trace_id, seq);
CREATE TABLE events(
  hop_id INTEGER NOT NULL, idx INTEGER NOT NULL, offset_us INTEGER, kind TEXT, name TEXT, data_blob TEXT,
  PRIMARY KEY(hop_id, idx));
CREATE TABLE corr(key TEXT NOT NULL, trace_id TEXT NOT NULL);
CREATE INDEX corr_key ON corr(key);
CREATE TABLE kv(k TEXT PRIMARY KEY, v TEXT);
`

// Open opens (creating if needed) the store rooted at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", URI(filepath.Join(dir, "customs.db"), "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, dir: dir, Now: time.Now}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(filepath.Join(dir, "customs.db"), 0o600)
	return s, nil
}

// URI builds a SQLite file URI that also works for Windows paths and paths with spaces.
func URI(path, query string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p, RawQuery: query}).String()
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	switch {
	case v == 0:
		if _, err := s.db.Exec(schema); err != nil {
			return err
		}
		_, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion))
		return err
	case v > schemaVersion:
		return fmt.Errorf("database schema %d is newer than this build (%d)", v, schemaVersion)
	}
	return nil
}

// PutBlob stores data and returns its hash; empty data has no hash.
func (s *Store) PutBlob(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	path := filepath.Join(s.dir, "blobs", h[:2], h+".gz")
	if _, err := os.Stat(path); err == nil {
		return h, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(data)
	_ = zw.Close()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	return h, os.Rename(tmp, path)
}

// Blob returns the bytes stored under hash.
func (s *Store) Blob(hash string) ([]byte, error) {
	if hash == "" {
		return nil, nil
	}
	if len(hash) < 3 || strings.ContainsAny(hash, "/\\.") {
		return nil, errors.New("bad blob hash")
	}
	f, err := os.Open(filepath.Join(s.dir, "blobs", hash[:2], hash+".gz"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

func us(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMicro()
}

func fromUS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMicro(v)
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// SaveHop inserts the hop (ID 0) or updates it, then refreshes its trace summary.
func (s *Store) SaveHop(ctx context.Context, h *model.Hop) error {
	reqBlob, err := s.PutBlob(h.ReqBody)
	if err != nil {
		return err
	}
	respBlob, err := s.PutBlob(h.RespBody)
	if err != nil {
		return err
	}
	args := []any{h.TraceID, h.Seq, h.Source, h.Tier, h.From, h.To, us(h.Started), us(h.FirstByte), us(h.Ended), h.Protocol,
		h.Method, h.URI, jsonStr(h.ReqHeader), reqBlob, h.Status, jsonStr(h.RespHdr), respBlob, boolInt(h.Streaming),
		h.Model, h.Finish, jsonStr(h.Usage), h.Error, jsonStr(h.Note), h.Events}
	if h.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO hops(trace_id,seq,source,tier,from_,to_,started_us,first_byte_us,ended_us,protocol,
			method,uri,req_header,req_blob,status,resp_header,resp_blob,streaming,model,finish,usage,error,note,events)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
		if err != nil {
			return err
		}
		h.ID, _ = res.LastInsertId()
	} else {
		_, err := s.db.ExecContext(ctx, `UPDATE hops SET trace_id=?,seq=?,source=?,tier=?,from_=?,to_=?,started_us=?,first_byte_us=?,ended_us=?,
			protocol=?,method=?,uri=?,req_header=?,req_blob=?,status=?,resp_header=?,resp_blob=?,streaming=?,model=?,finish=?,usage=?,
			error=?,note=?,events=? WHERE id=?`, append(args, h.ID)...)
		if err != nil {
			return err
		}
	}
	return s.RefreshTrace(ctx, h.TraceID)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// AddEvents appends stream events to a hop.
func (s *Store) AddEvents(ctx context.Context, evs []model.Event) error {
	if len(evs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range evs {
		blob, err := s.PutBlob(e.Data)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO events(hop_id,idx,offset_us,kind,name,data_blob) VALUES(?,?,?,?,?,?)`,
			e.HopID, e.Idx, e.OffsetUS, e.Kind, e.Name, blob); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Link records a correlation key for a trace.
func (s *Store) Link(ctx context.Context, key, traceID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO corr(key,trace_id) VALUES(?,?)`, key, traceID)
	return err
}

// TraceByKey finds a trace by a correlation key.
func (s *Store) TraceByKey(ctx context.Context, key string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT trace_id FROM corr WHERE key=? ORDER BY rowid DESC LIMIT 1`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// KV stores small values such as collector cursors.
func (s *Store) SetKV(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

func (s *Store) GetKV(ctx context.Context, k string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
