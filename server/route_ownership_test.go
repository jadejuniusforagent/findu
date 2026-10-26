package server

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"finduai/finduapp"
)

// recordedRoutes 只记录 pattern，不执行 handler，用于在无 DB / 无插件的情况下
// 取得完整的宿主 /api 路由表。
type recordedRoutes struct {
	patterns []string
}

func (r *recordedRoutes) HandleFunc(pattern string, _ func(http.ResponseWriter, *http.Request)) {
	r.patterns = append(r.patterns, pattern)
}

// apiPrefix 把 "GET /api/tasks/{id}" 归并到 "/api/tasks"。
// 只取第一段资源名，因为 findu 的兜底判定也是按第一段前缀匹配的。
func apiPrefix(pattern string) string {
	i := strings.Index(pattern, "/api/")
	if i < 0 {
		return ""
	}
	p := pattern[i:]
	rest := strings.TrimPrefix(p, "/api/")
	if rest == "" {
		return ""
	}
	first := strings.SplitN(rest, "/", 2)[0]
	if first == "" {
		return ""
	}
	return "/api/" + first
}

func hostAPIRoutes(t *testing.T) map[string][]string {
	t.Helper()
	rec := &recordedRoutes{}
	// registerRoutes 只绑定 handler，不访问 Server 字段；plugins 为 nil 时
	// registerPluginRoutes 直接返回，因此插件路由不在此快照里。
	(&Server{}).registerRoutes(rec)
	out := make(map[string][]string)
	for _, p := range rec.patterns {
		prefix := apiPrefix(p)
		if prefix == "" {
			continue
		}
		out[prefix] = append(out[prefix], p)
	}
	for prefix := range out {
		sort.Strings(out[prefix])
	}
	return out
}

// documentedHostShadows 登记「宿主已注册、但当前被 findu 前缀整体遮蔽」的资源。
//
// 这不是允许长期并存，而是迁移期的显式欠账：每个条目都要在 Step 6 的页面迁移
// 批次里消掉。新增一条就会让契约测试失败，迫使作者先决定归属，而不是让宿主
// 路由悄悄变成死代码。
var documentedHostShadows = map[string]string{}

func TestAPIRouteOwnershipContract(t *testing.T) {
	host := hostAPIRoutes(t)
	finduOwned := finduapp.OwnedAPIPaths()

	observed := make(map[string][]string)
	for _, prefix := range finduOwned {
		if len(host[prefix]) == 0 {
			continue
		}
		observed[prefix] = host[prefix]
	}

	for prefix, patterns := range observed {
		if _, ok := documentedHostShadows[prefix]; !ok {
			t.Errorf("%s is findu-owned and shadows %d host route(s), but is not in documentedHostShadows: %v",
				prefix, len(patterns), patterns)
		}
	}
	for prefix, reason := range documentedHostShadows {
		patterns, ok := observed[prefix]
		if !ok {
			t.Errorf("documentedHostShadows[%q] is stale: prefix is not findu-owned or has no host route", prefix)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("documentedHostShadows[%q] has no migration note", prefix)
		}
		if len(patterns) == 0 {
			t.Errorf("documentedHostShadows[%q] has no host routes", prefix)
		}
	}
}

func TestMultiAgentRoutesStayHostOwned(t *testing.T) {
	host := hostAPIRoutes(t)
	patterns := host["/api/multi-agent"]
	if len(patterns) < 2 {
		t.Fatalf("/api/multi-agent host routes = %v, want both bare and subtree patterns", patterns)
	}
	for _, p := range finduapp.OwnedAPIPaths() {
		if PathOwnedBy("/api/multi-agent", []string{p}) {
			t.Fatalf("/api/multi-agent is covered by findu prefix %q", p)
		}
	}
}
