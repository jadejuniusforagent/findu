package app

import "net/http"

// Handler 暴露内部 gin 路由，供外部宿主（net/http mux）
// 以子路径挂载使用。迁移期（Strangler）保持 findu 自己的路由表不变。
func (a *App) Handler() http.Handler {
	return a.router
}
