package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vist233/customs-llm/internal/relay"
	"github.com/Vist233/customs-llm/internal/store"
)

func server(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{Store: st, Relay: relay.New(st), Hub: NewHub(), Token: "tok"}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func do(t *testing.T, method, url string, hdr map[string]string, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestGuards(t *testing.T) {
	_, ts := server(t)
	if c, _ := do(t, "POST", ts.URL+"/api/targets", nil, `{}`); c != 401 {
		t.Errorf("mutation without token: %d", c)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/status", nil)
	req.Host = "evil.example"
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("foreign host: %d", resp.StatusCode)
	}
}

func TestPlanNeedsWebApproval(t *testing.T) {
	s, ts := server(t)
	cli := map[string]string{"X-Customs-Token": "tok"}
	c, body := do(t, "POST", ts.URL+"/api/plans", cli, `{"driver":"litellm","actions":["add x"]}`)
	if c != 200 {
		t.Fatalf("%d %s", c, body)
	}
	var p Plan
	_ = json.Unmarshal([]byte(body), &p)
	if c, _ := do(t, "POST", ts.URL+"/api/plans/"+p.ID+"/consume", cli, ""); c != 409 {
		t.Errorf("pending plan consumed: %d", c)
	}
	if c, _ := do(t, "POST", ts.URL+"/api/plans/"+p.ID+"/approve", cli, ""); c != 403 {
		t.Errorf("CLI token approved a plan: %d", c)
	}
	web := map[string]string{"X-Customs-Token": "tok", "X-Customs-UI-Token": s.uiToken(), "Sec-Fetch-Site": "same-origin"}
	if c, _ := do(t, "POST", ts.URL+"/api/plans/"+p.ID+"/approve", web, ""); c != 200 {
		t.Errorf("web approval: %d", c)
	}
	if c, _ := do(t, "POST", ts.URL+"/api/plans/"+p.ID+"/consume", cli, ""); c != 200 {
		t.Errorf("consume: %d", c)
	}
	if c, _ := do(t, "POST", ts.URL+"/api/plans/"+p.ID+"/consume", cli, ""); c != 409 {
		t.Errorf("plan used twice: %d", c)
	}
}
