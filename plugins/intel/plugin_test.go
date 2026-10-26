package intel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dfe "finduai/domain/finding"

	"finduai/core/pluginhost"
)

var errNotFound = errors.New("not found")

type fakeIntel struct {
	started   int
	stopped   int
	refreshed int
	statusAt  time.Time
}

func (f *fakeIntel) Start() { f.started++ }
func (f *fakeIntel) Stop()  { f.stopped++ }
func (f *fakeIntel) Refresh() {
	f.refreshed++
}
func (f *fakeIntel) Status() (bool, time.Time, any, int) {
	return true, f.statusAt, map[string]int{"new": 2}, 9
}

type fakeStore struct {
	kb        []dfe.VulnKBEntry
	intel     []dfe.EnrichedVulnIntel
	lastQuery string
	lastType  string
	lastPage  int
	lastSize  int
	getErr    error
	listErr   error
	statsErr  error
	intelErr  error
}

func (f *fakeStore) ListVulnKB(q, typ string, page, size int) ([]dfe.VulnKBEntry, int, error) {
	f.lastQuery, f.lastType, f.lastPage, f.lastSize = q, typ, page, size
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	return f.kb, len(f.kb), nil
}

func (f *fakeStore) GetVulnKB(id int64) (*dfe.VulnKBEntry, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if len(f.kb) == 0 {
		return nil, errNotFound
	}
	return &f.kb[0], nil
}

func (f *fakeStore) VulnKBStats() (*dfe.VulnKBStats, error) {
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	return &dfe.VulnKBStats{Total: len(f.kb), Types: []dfe.VulnKBTypeCount{}}, nil
}

func (f *fakeStore) ListVulnIntel(q, severity string, page, size int) ([]dfe.EnrichedVulnIntel, int, error) {
	if f.intelErr != nil {
		return nil, 0, f.intelErr
	}
	return f.intel, len(f.intel), nil
}

func newTestPlugin(t *testing.T, deps Deps) *Plugin {
	t.Helper()
	p, err := New(deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestManifestAndLifecycle(t *testing.T) {
	var bootstraps int
	pipeline := &fakeIntel{}
	p := newTestPlugin(t, Deps{
		Store:     &fakeStore{},
		Bootstrap: func() { bootstraps++ },
		Intel:     pipeline,
	})

	m := p.Manifest()
	if m.ID != "intel" || m.Kind != pluginhost.KindDataset || m.APIVersion != pluginhost.APIVersion {
		t.Fatalf("清单不对: %+v", m)
	}
	if len(m.OwnsTables) != 4 {
		t.Fatalf("owns_tables=%v", m.OwnsTables)
	}
	if m.Provides == nil || len(m.Provides.Datasets) != 4 || len(m.Provides.Routes) != 6 {
		t.Fatalf("provides=%+v", m.Provides)
	}

	ctx := context.Background()
	if err := p.Health(ctx); err == nil {
		t.Fatal("未启动时 Health 应失败")
	}
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Health(ctx); err != nil {
		t.Fatalf("启动后 Health: %v", err)
	}
	if bootstraps != 1 {
		t.Fatalf("首次 Start 应触发一次导入，实际 %d", bootstraps)
	}
	if pipeline.started != 1 {
		t.Fatalf("首次 Start 应启动情报管线，实际 %d", pipeline.started)
	}
	if err := p.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if pipeline.stopped != 1 {
		t.Fatalf("Stop 应停止情报管线，实际 %d", pipeline.stopped)
	}
	if err := p.Health(ctx); err == nil {
		t.Fatal("停止后 Health 应失败")
	}
	// 重启不重复导入（已导入是幂等的，但没必要再遍历一次目录）。
	if err := p.Start(ctx); err != nil {
		t.Fatalf("重启: %v", err)
	}
	if bootstraps != 1 {
		t.Fatalf("重启不应重复导入，实际 %d", bootstraps)
	}
	if pipeline.started != 2 {
		t.Fatalf("重启应再次启动情报管线，实际 %d", pipeline.started)
	}
}

func TestRoutes(t *testing.T) {
	p := newTestPlugin(t, Deps{Store: &fakeStore{}})
	routes := p.Routes()
	if len(routes) != 6 {
		t.Fatalf("routes=%d", len(routes))
	}
	want := map[string]bool{
		"GET /api/vulnkb": false, "GET /api/vulnkb/stats": false, "GET /api/vulnkb/{id}": false,
		"GET /api/vulnintel": false, "POST /api/vulnintel/refresh": false, "GET /api/vulnintel/status": false,
	}
	for _, rt := range routes {
		if _, ok := want[rt.Pattern]; !ok {
			t.Fatalf("未知路由 %s", rt.Pattern)
		}
		want[rt.Pattern] = true
	}
	for pat, seen := range want {
		if !seen {
			t.Fatalf("缺少路由 %s", pat)
		}
	}
}

func TestHTTPHandlers(t *testing.T) {
	store := &fakeStore{kb: []dfe.VulnKBEntry{{ID: 7, Title: "demo", VulnType: "rce"}}}
	statusAt := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	pipeline := &fakeIntel{statusAt: statusAt}
	p := newTestPlugin(t, Deps{Store: store, Intel: pipeline})

	hit := func(method, target string, h http.HandlerFunc) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, target, nil))
		return rec
	}

	rec := hit("GET", "/api/vulnkb?q=demo&type=rce&page=2&size=5", p.listVulnKB)
	if rec.Code != 200 {
		t.Fatalf("list code=%d body=%s", rec.Code, rec.Body)
	}
	if store.lastQuery != "demo" || store.lastType != "rce" || store.lastPage != 2 || store.lastSize != 5 {
		t.Fatalf("参数未透传: %+v", store)
	}
	var listResp struct {
		Entries []dfe.VulnKBEntry `json:"entries"`
		Total   int               `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil || listResp.Total != 1 {
		t.Fatalf("list resp=%s err=%v", rec.Body, err)
	}

	rec = hit("GET", "/api/vulnkb/stats", p.vulnKBStats)
	if rec.Code != 200 || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("stats code=%d body=%s", rec.Code, rec.Body)
	}

	// 路径参数由宿主 mux 填充；单测里直接 SetPathValue。
	req := httptest.NewRequest("GET", "/api/vulnkb/7", nil)
	req.SetPathValue("id", "7")
	rec = httptest.NewRecorder()
	p.getVulnKB(rec, req)
	if rec.Code != 200 || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("get code=%d body=%s", rec.Code, rec.Body)
	}

	rec = hit("GET", "/api/vulnintel?q=x&severity=high", p.listVulnIntel)
	if rec.Code != 200 {
		t.Fatalf("intel code=%d body=%s", rec.Code, rec.Body)
	}
	rec = hit("POST", "/api/vulnintel/refresh", p.refreshVulnIntel)
	if rec.Code != 200 || pipeline.refreshed != 1 {
		t.Fatalf("refresh code=%d refreshed=%d", rec.Code, pipeline.refreshed)
	}
	rec = hit("GET", "/api/vulnintel/status", p.vulnIntelStatus)
	if rec.Code != 200 {
		t.Fatalf("status code=%d body=%s", rec.Code, rec.Body)
	}
	var st map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st["polling"] != true || st["last"] != statusAt.Format(time.RFC3339) || st["sources"] != float64(9) {
		t.Fatalf("status=%v", st)
	}

	// 缺失资源与非法 id 的错误码保持迁移前的行为。
	empty := newTestPlugin(t, Deps{Store: &fakeStore{}})
	req = httptest.NewRequest("GET", "/api/vulnkb/9", nil)
	req.SetPathValue("id", "9")
	rec = httptest.NewRecorder()
	empty.getVulnKB(rec, req)
	if rec.Code != 404 {
		t.Fatalf("missing code=%d", rec.Code)
	}
	req.SetPathValue("id", "bad")
	rec = httptest.NewRecorder()
	empty.getVulnKB(rec, req)
	if rec.Code != 400 {
		t.Fatalf("bad id code=%d", rec.Code)
	}
}

func TestHTTPHandlerErrors(t *testing.T) {
	store := &fakeStore{
		listErr:  errors.New("list down"),
		statsErr: errors.New("stats down"),
		intelErr: errors.New("intel down"),
		getErr:   errNotFound,
	}
	p := newTestPlugin(t, Deps{Store: store})

	hit := func(method, target string, h http.HandlerFunc) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, target, nil))
		return rec
	}

	cases := []struct {
		name string
		h    http.HandlerFunc
		code int
	}{
		{"list kb", p.listVulnKB, 500},
		{"stats", p.vulnKBStats, 500},
		{"list intel", p.listVulnIntel, 500},
	}
	for _, c := range cases {
		if rec := hit("GET", "/api/x", c.h); rec.Code != c.code {
			t.Fatalf("%s code=%d body=%s", c.name, rec.Code, rec.Body)
		}
	}

	req := httptest.NewRequest("GET", "/api/vulnkb/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	p.getVulnKB(rec, req)
	if rec.Code != 404 {
		t.Fatalf("get 错误应 404，实际 %d", rec.Code)
	}

	// 情报管线未接入：refresh/status 返回 503，而不是空成功。
	bare := newTestPlugin(t, Deps{Store: &fakeStore{}})
	if rec := hit("POST", "/api/vulnintel/refresh", bare.refreshVulnIntel); rec.Code != 503 {
		t.Fatalf("refresh 未接入 code=%d", rec.Code)
	}
	if rec := hit("GET", "/api/vulnintel/status", bare.vulnIntelStatus); rec.Code != 503 {
		t.Fatalf("status 未接入 code=%d", rec.Code)
	}
}

func TestStartWithoutBootstrap(t *testing.T) {
	p := newTestPlugin(t, Deps{Store: &fakeStore{}})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func TestNewRequiresStore(t *testing.T) {
	if _, err := New(Deps{}); err == nil {
		t.Fatal("未注入 Store 应失败")
	}
}
