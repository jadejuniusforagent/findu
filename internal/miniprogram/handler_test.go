package miniprogram

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// TestHandler_RegisterRoutes 校验路由注册无 panic/冲突
// （Gin 对静态路由与 wildcard 并存的场景敏感，注册即验证）。
func TestHandler_RegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandler(nil) // 仅注册路由，不触发业务
	h.Register(r.Group("/api"))

	routes := r.Routes()
	want := map[string]bool{
		"POST /api/miniprogram/upload":               false,
		"POST /api/miniprogram/samples":              false,
		"POST /api/miniprogram/samples/:id":          false,
		"DELETE /api/miniprogram/samples/:id":        false,
		"POST /api/miniprogram/samples/:id/source":   false,
		"GET /api/miniprogram/samples/:id/file":      false,
		"POST /api/miniprogram/samples/:id/findings": false,
		"POST /api/miniprogram/samples/:id/scan":     false,
		"POST /api/miniprogram/handoff":              false,
	}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("路由未注册: %s", key)
		}
	}
}
