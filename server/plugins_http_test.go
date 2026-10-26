package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"finduai/core/pluginhost"
	"finduai/core/provider"
	"finduai/core/provider/mock"
)

type stubPlugin struct{ m pluginhost.Manifest }

func (p stubPlugin) Manifest() pluginhost.Manifest { return p.m }
func (p stubPlugin) Start(context.Context) error   { return nil }
func (p stubPlugin) Stop(context.Context) error    { return nil }
func (p stubPlugin) Health(context.Context) error  { return nil }

func TestPluginsEndpoint(t *testing.T) {
	s := &Server{
		plugins:   pluginhost.NewRegistry(pluginhost.APIVersion),
		providers: provider.NewRegistry(),
	}
	hit := func() (int, struct {
		Plugins   []pluginStatusJSON `json:"plugins"`
		Providers map[string]string  `json:"providers"`
	}) {
		rec := httptest.NewRecorder()
		s.getPlugins(rec, httptest.NewRequest("GET", "/api/plugins", nil))
		var out struct {
			Plugins   []pluginStatusJSON `json:"plugins"`
			Providers map[string]string  `json:"providers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
		}
		return rec.Code, out
	}

	code, out := hit()
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	if len(out.Plugins) != 0 {
		t.Fatalf("空注册表 plugins=%+v", out.Plugins)
	}
	if out.Providers == nil || len(out.Providers) != 0 {
		t.Fatalf("空注册表 providers=%v", out.Providers)
	}

	// 注册一个最小插件后，接口应能看到状态与版本（批 2 不做真实加载）。
	if err := s.plugins.Register(stubPlugin{m: pluginhost.Manifest{
		ID: "intel", Version: "1.0.0", Kind: pluginhost.KindDataset,
		APIVersion: pluginhost.APIVersion, Provides: &pluginhost.Provides{},
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// 能力注册表同样可见：批 2 用内存 mock 验证「装配 -> 展示」链路。
	if err := s.providers.RegisterC2("mock-c2", mock.NewC2()); err != nil {
		t.Fatalf("RegisterC2: %v", err)
	}
	code, out = hit()
	if code != 200 || len(out.Plugins) != 1 {
		t.Fatalf("code=%d plugins=%+v", code, out.Plugins)
	}
	p := out.Plugins[0]
	if p.ID != "intel" || p.Version != "1.0.0" || p.Kind != "dataset" || p.State != "registered" || p.Error != "" {
		t.Fatalf("插件快照不对: %+v", p)
	}
	if out.Providers["c2"] != "mock-c2" {
		t.Fatalf("providers 绑定不对: %v", out.Providers)
	}
}
