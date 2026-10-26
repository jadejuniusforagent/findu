package server

// scope_gate 把 capkit 式的逐目标门禁接入宿主：工具索引（guard 探测 Targeted/
// SideEffector 可选扩展用）+ 任务授权范围校验器（task_scope 落地）+ policy 开关。
//
// 三条硬规则（都是刻意选择，不是偷懒）：
//  1. 默认影子模式：一切违规只记 warn，不拦截。P4 迁移期 agent 行为不能被门禁
//     翻车污染，先积累 warn 分布，settings 翻 true 才逐目标硬拦。
//  2. 拿不到裁定依据就放行并留痕：task_scope 查询失败 / 表为空 / 只有 company/
//     icp/keyword 这类无法逐目标核对的行 → 不拒绝（拒绝会杀死所有任务），但一次性
//     warn。fail closed 只作用于「明确越界」与「目标解析失败」，不作用于门禁自身故障。
//  3. 限速键 = 目标主机（Target.Host()），来自 ToolConfig 的 targets 声明或常见参数
//     名兜底；没有目标声明能力的工具限不到——那是审计与拦截规则的职责。
//
// 契约对齐：contracts/tool-contract.schema.json 的 targets / side_effect 字段；
// 本文件同时是 docs/refactor/hardening-design.md 的实现锚点。

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"finduai/core/log"
	pgdb "finduai/db"
	"finduai/guard"
	actool "finduai/tools"
)

// settings keys（与 manager.go 的常量块同风格）。
const (
	settingGuardScopeEnforce = "guard_scope_enforce"       // "true" 才逐目标硬拦；默认影子
	settingGuardRateLimit    = "guard_rate_limit_per_host" // 正整数 = 每主机每分钟上限；0/缺省关
	settingGuardRateEnforce  = "guard_rate_limit_enforce"  // "true" 超限硬拦；默认影子
)

// --- policy 烘焙（Manager per task） ---

// SetTargetResolver 注入工具索引，guard 靠它对每次调用探测 Targeted/SideEffector。
// Server 在 WireGuardPolicy 里调用一次；guard 读侧有独立锁，运行期换索引安全。
func (m *Manager) SetTargetResolver(fn guard.TargetResolver) {
	m.targetResolver.Store(fn)
}

// bakeGuardPolicy 把门禁资源烘进任务的 Guard。建任务/重载任务时各执行一次；
// 开关从 settings 实时读——改配置影响之后创建的任务，无需重启。
func (m *Manager) bakeGuardPolicy(t *Task) {
	if t.Guard == nil {
		return
	}
	if fn, ok := m.targetResolver.Load().(guard.TargetResolver); ok && fn != nil {
		t.Guard.SetTargetResolver(fn)
	}
	t.Guard.SetScopeChecker(m.taskScopeChecker(t.ID), m.guardScopeEnforce())
	if per, enforce := m.guardRateLimit(); per > 0 {
		t.Guard.SetRateLimit(per, enforce)
	}
}

func (m *Manager) guardScopeEnforce() bool {
	v, _, _ := m.pg.GetSetting(settingGuardScopeEnforce)
	return strings.TrimSpace(v) == "true"
}

func (m *Manager) guardRateLimit() (perMinute int, enforce bool) {
	v, ok, _ := m.pg.GetSetting(settingGuardRateLimit)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0, false
	}
	e, _, _ := m.pg.GetSetting(settingGuardRateEnforce)
	return n, strings.TrimSpace(e) == "true"
}

// --- 任务授权范围校验器 ---

// scopeCheckerCache 让逐次工具调用的 task_scope 查询有 30s 红利窗口：范围行在任务
// 生命周期里是缓慢变化的真值（不是热数据），没必要每个工具调用都 SELECT。
type scopeCheckerCache struct {
	loader  func() ([]pgdb.TaskScope, error)
	mu      sync.Mutex
	rows    []pgdb.TaskScope
	err     error
	fetched time.Time
}

func (c *scopeCheckerCache) rows30s() ([]pgdb.TaskScope, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fetched.IsZero() || time.Since(c.fetched) > 30*time.Second {
		rows, loadErr := c.loader()
		c.rows, c.err, c.fetched = rows, loadErr, time.Now()
	}
	if c.err != nil {
		return nil, c.err
	}
	return c.rows, nil
}

// warnOnceMap 按 key 去重的一次性影子告警。
type warnOnceMap struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (w *warnOnceMap) once(key string, format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	if w.seen[key] {
		return
	}
	w.seen[key] = true
	log.Printf("[scope] "+format, args...)
}

// taskScopeChecker 返回该任务的逐目标授权校验器（guard.ScopeChecker）。
func (m *Manager) taskScopeChecker(taskIDStr string) guard.ScopeChecker {
	taskID, err := strconv.ParseInt(taskIDStr, 10, 64)
	if err != nil || taskID <= 0 {
		return nil // 任务 id 解析不了（异常 handle）：不挂校验器，观测层兜底
	}
	cache := &scopeCheckerCache{loader: func() ([]pgdb.TaskScope, error) {
		return m.assets.ListTaskScope(taskID)
	}}
	warns := &warnOnceMap{}
	return func(ctx context.Context, tool string, targets []actool.Target) error {
		rows, err := cache.rows30s()
		if err != nil {
			// 门禁数据源故障 ≠ 目标越界：放行 + 留痕（文件头规则 2）。
			warns.once("db", "task %d 的授权范围查询失败（影子放行）: %v", taskID, err)
			return nil
		}
		if len(rows) == 0 {
			warns.once("empty", "task %d 还没有任何授权范围声明（影子放行）", taskID)
			return nil
		}
		verbatim, ok := verifiableScopeRows(rows)
		if !ok {
			warns.once("unverifiable", "task %d 的范围只有 company/icp/keyword 行，无法逐目标核对（影子放行）", taskID)
			return nil
		}
		for _, tg := range targets {
			host := tg.Host()
			if !scopeMatch(verbatim, host) {
				return fmt.Errorf("目标 %s 不在任务 #%d 的授权范围内（匹配规则 root_domain/subdomain/ip/cidr； 如确属范围内目标，请在任务页用 add_task_scope 申明范围后重试）", host, taskID)
			}
		}
		return nil
	}
}

// verifiableScopeRows 拆分可逐目标核对的范围行（root_domain/subdomain/ip/cidr）与
// 不可核对的（company/icp/keyword）。只要存在任一可核对行，校验就按 fail closed
// 裁决（未命中任何行 = 越界）；否则返回 false，由调用方按「无法裁决 → 放行」处理。
func verifiableScopeRows(rows []pgdb.TaskScope) (verbatim []pgdb.TaskScope, ok bool) {
	for _, ts := range rows {
		switch ts.Kind {
		case "root_domain", "subdomain", "ip", "cidr":
			verbatim = append(verbatim, ts)
		}
	}
	return verbatim, len(verbatim) > 0
}

// scopeMatch 判目标 host 是否落在范围行内（Go 侧复刻 task_scope→assets 匹配谓词）：
//   - root_domain 行：host == domain 或 host 以 ".domain" 结尾（资产 root_domain 语义）
//   - subdomain 行：精确相等（资产 domain 语义）
//   - ip 行：net 容忍裸 IP 与 x.x.x.x/32 两种存储写法
//   - cidr 行：host 解析为 IP 且落在 net 内
//
// company 行由 insert_assets 预展开成 asset 级行，这里不重复展开；icp/keyword 行无
// 目标侧等价形态，不参与（见 verifiableScopeRows 的放行规则）。
func scopeMatch(rows []pgdb.TaskScope, host string) bool {
	ipT := net.ParseIP(host)
	for _, ts := range rows {
		switch ts.Kind {
		case "root_domain":
			if domainIn(host, ts.Domain) {
				return true
			}
		case "subdomain":
			if strings.EqualFold(host, ts.Domain) {
				return true
			}
		case "ip":
			if ipT != nil && ts.Net != "" {
				bare := strings.TrimSuffix(ts.Net, "/32")
				if strings.EqualFold(bare, host) {
					return true
				}
				if _, ipNet, err := net.ParseCIDR(ts.Net); err == nil && ipNet.Contains(ipT) {
					return true
				}
			}
		case "cidr":
			if ipT != nil && ts.Net != "" {
				if _, ipNet, err := net.ParseCIDR(ts.Net); err == nil && ipNet.Contains(ipT) {
					return true
				}
			}
		}
	}
	return false
}

// domainIn：host 与 domain 相等，或 host 是 domain 的子域。
func domainIn(host, domain string) bool {
	domain = strings.ToLower(strings.Trim(domain, "."))
	host = strings.ToLower(host)
	return domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}

// --- Server 工具索引（guard 探测可选扩展用） ---

// toolIndexUnion 是进程级工具索引缓存：guard 每次 PreToolUse 都要做一次
// name→CoreTool 查找，不能每次都重建 hostTools()（里面有 DB 列表调用）。
// 单 Server per process，这里用包级单例是务实选择（注释留痕，不算隐瞒）。
type toolIndexUnion struct {
	mu     sync.RWMutex
	byName map[string]actool.CoreTool
	loaded time.Time
}

var toolIndex = &toolIndexUnion{}

func (u *toolIndexUnion) get(name string) (actool.CoreTool, bool) {
	u.mu.RLock()
	t, ok := u.byName[name]
	u.mu.RUnlock()
	return t, ok
}

func (u *toolIndexUnion) rebuild(s *Server) {
	ht, _ := s.hostTools()
	all := append([]actool.CoreTool(nil), actool.DefaultTools()...)
	all = append(all, ht...)
	idx := map[string]actool.CoreTool{}
	for _, t := range all {
		if t != nil && t.Name() != "" {
			idx[t.Name()] = t
		}
	}
	u.mu.Lock()
	u.byName, u.loaded = idx, time.Now()
	u.mu.Unlock()
}

func (u *toolIndexUnion) reloadIfStale(s *Server) {
	u.mu.RLock()
	fresh := time.Since(u.loaded) < 60*time.Second
	u.mu.RUnlock()
	if !fresh {
		u.rebuild(s)
	}
}

// WireGuardPolicy 在 Server 侧装配门禁资源：工具索引注进 Manager。
// 由 main 在 SetRecipeTools 之后调用一次；配方/平台工具后续变化由 60s TTL 反映。
func (s *Server) WireGuardPolicy() {
	toolIndex.rebuild(s) // 立即建一次索引，首个任务就有门禁扩展可见
	s.m.SetTargetResolver(func(name string) (actool.CoreTool, bool) {
		toolIndex.reloadIfStale(s)
		return toolIndex.get(name)
	})
}
