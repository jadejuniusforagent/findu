// Package routereg 维护「POST 承载查询参数」路由的全局注册表（查询路由白名单）。
//
// 业务查询接口以 POST + JSON body + X-Api-Query-Post 标记头承载原 query 参数
// （见 app 包 postQueryAdapter）。适配器把这类请求改写成等价 GET 放行——
// RBAC 沿用 GET 读权限映射、CSRF 跳过。改写必须严格限定在查询路由内：
// 真实写接口（创建/删除/执行类 POST）一旦被改写，RBAC 会按 GET 误映射为读权限、
// CSRF 也会跳过，只读会话即可调用写接口。因此查询路由必须经 QueryPOST 注册，
// 适配器只对注册表内的 pattern 改写（fail-closed：白名单外一律原样放行）。
package routereg

import (
	"path"
	"sort"
	"sync"

	"github.com/gin-gonic/gin"
)

var (
	mu     sync.RWMutex
	routes = make(map[string]struct{})
)

// QueryPOST 注册一条查询路由并加入适配器白名单。handlers 透传 gin，用法与
// g.POST 完全一致（可挂 RequirePermission 等中间件）。
func QueryPOST(g *gin.RouterGroup, relativePath string, handlers ...gin.HandlerFunc) {
	g.POST(relativePath, handlers...)
	full := joinPaths(g.BasePath(), relativePath)
	mu.Lock()
	routes[full] = struct{}{}
	mu.Unlock()
}

// IsQueryRoute 返回完整路由 pattern（gin 的 c.FullPath()）是否为已注册查询路由。
func IsQueryRoute(fullPath string) bool {
	if fullPath == "" {
		return false
	}
	mu.RLock()
	defer mu.RUnlock()
	_, ok := routes[fullPath]
	return ok
}

// QueryRoutes 返回全部已注册查询路由 pattern（排序），供 e2e 与路由表交叉校验。
func QueryRoutes() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(routes))
	for p := range routes {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Reset 清空注册表。仅供测试隔离：e2e 装配被测应用前调用，避免单测夹具路由
// （不挂在被测路由表上）混进白名单交叉校验。
func Reset() {
	mu.Lock()
	routes = make(map[string]struct{})
	mu.Unlock()
}

// joinPaths 与 gin 注册路由的绝对路径拼接规则保持一致（相对路径为空返回组基路径）。
func joinPaths(base, rel string) string {
	if rel == "" {
		return base
	}
	return path.Join(base, rel)
}
