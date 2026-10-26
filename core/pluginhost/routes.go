package pluginhost

import "net/http"

// Route 是插件贡献给宿主 apiMux 的一条路由。Pattern 使用 Go 1.22 的
// "METHOD /path/{param}" 形式，与 server/routes.go 的写法一致。
//
// 宿主负责把路由挂到自己的 mux 上，并在调用前检查插件状态：插件停止后同一
// 路径返回 503，而不是 404——「入口还在、实现下线」比「路径凭空消失」更
// 容易让前端和运维判断当前状态。
type Route struct {
	Pattern string
	Handler http.HandlerFunc
}

// RouteProvider 是可选接口。实现它的插件在启动后把路由交给宿主注册。
//
// 插件不直接持有 *http.ServeMux（那会把宿主结构泄漏进插件），只声明
// 「模式 -> handler」。鉴权、CORS 与前端回退仍由宿主统一处理。
type RouteProvider interface {
	Routes() []Route
}
