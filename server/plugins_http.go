package server

import (
	"net/http"

	"finduai/core/pluginhost"
)

// getPlugins 返回插件宿主与能力注册表的只读快照。
//
// 批 2 只做展示：注册表为空是正常状态（还没有真实插件）。批 3 起插件在启动
// 装配阶段注册，这里能看到状态、版本与「能力 -> 插件 id」绑定。
func (s *Server) getPlugins(w http.ResponseWriter, r *http.Request) {
	statuses := []pluginStatusJSON{}
	if s.plugins != nil {
		for _, st := range s.plugins.List() {
			statuses = append(statuses, pluginStatusJSON{
				ID: st.ID, Version: st.Version, Kind: string(st.Kind), State: string(st.State), Error: st.Error,
			})
		}
	}
	bindings := map[string]string{}
	if s.providers != nil {
		for cap, id := range s.providers.Bindings() {
			bindings[string(cap)] = id
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plugins":   statuses,
		"providers": bindings,
	})
}

// pluginStatusJSON 是 /api/plugins 的稳定输出结构；不直接暴露 pluginhost.Status，
// 避免内部枚举变更破坏前端契约。
type pluginStatusJSON struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
}

// registerPluginRoutes 把实现了 pluginhost.RouteProvider 的已注册插件的路由
// 挂到宿主 mux 上。路由在 Handler() 构建时静态注册一次；插件停止后 handler
// 外层的状态检查返回 503（见 pluginRoute），因此不需要动态增删路由。
func (s *Server) registerPluginRoutes(mux routeRegistrar) {
	if s.plugins == nil {
		return
	}
	for _, st := range s.plugins.List() {
		p, ok := s.plugins.Get(st.ID)
		if !ok {
			continue
		}
		rp, ok := p.(pluginhost.RouteProvider)
		if !ok {
			continue
		}
		for _, rt := range rp.Routes() {
			if rt.Pattern == "" || rt.Handler == nil {
				continue
			}
			mux.HandleFunc(rt.Pattern, s.pluginRoute(st.ID, rt.Handler))
		}
	}
}

// pluginRoute 在调用插件 handler 前检查插件状态。503 而不是 404：路径仍然
// 属于宿主 API，只是当前实现下线了。
func (s *Server) pluginRoute(id string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, ok := s.plugins.Status(id)
		if !ok || st.State != pluginhost.StateStarted {
			writeErr(w, http.StatusServiceUnavailable, "插件未启动: "+id)
			return
		}
		h(w, r)
	}
}
