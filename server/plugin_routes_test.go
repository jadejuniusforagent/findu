package server

import (
	"context"
	"encoding/json"
	"testing"
)

// 批 3 的端到端验收：插件注册 -> 路由可见 -> 停止后 503 -> 重启恢复。
// 数据读取走真实 PG（测试库），但不依赖 vulnkb 目录是否导入。
func TestIntelPluginRoutesLifecycle(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)

	rec := request("GET", "/api/vulnkb?size=1", "")
	if rec.Code != 200 {
		t.Fatalf("vulnkb code=%d body=%s", rec.Code, rec.Body.String())
	}
	var kb struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &kb); err != nil {
		t.Fatalf("vulnkb 响应不是 JSON: %v (%s)", err, rec.Body.String())
	}

	rec = request("GET", "/api/plugins", "")
	if rec.Code != 200 {
		t.Fatalf("plugins code=%d", rec.Code)
	}
	var plugins struct {
		Plugins []pluginStatusJSON `json:"plugins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plugins); err != nil {
		t.Fatalf("plugins 响应: %v", err)
	}
	found := false
	for _, p := range plugins.Plugins {
		if p.ID == "intel" {
			found = true
			if p.State != "started" {
				t.Fatalf("intel 状态=%s", p.State)
			}
		}
	}
	if !found {
		t.Fatalf("/api/plugins 未列出 intel: %+v", plugins.Plugins)
	}

	// 停止插件：同一路径由宿主状态检查返回 503，而不是 404。
	if err := s.plugins.Stop(context.Background(), "intel"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	rec = request("GET", "/api/vulnkb", "")
	if rec.Code != 503 {
		t.Fatalf("停止后 code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = request("GET", "/api/vulnintel/status", "")
	if rec.Code != 503 {
		t.Fatalf("停止后 intel status code=%d", rec.Code)
	}

	// 重启恢复，且不会重复触发目录导入（由插件内部保证）。
	if err := s.plugins.Start(context.Background(), "intel"); err != nil {
		t.Fatalf("重启: %v", err)
	}
	rec = request("GET", "/api/vulnkb/stats", "")
	if rec.Code != 200 {
		t.Fatalf("重启后 code=%d body=%s", rec.Code, rec.Body.String())
	}
}
