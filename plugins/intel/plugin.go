// Package intel 是第一个真实插件：漏洞知识库（1day）与漏洞情报的 HTTP 入口。
//
// 迁移范围：
//   - Step 4 批 3：六条 /api/vulnkb*、/api/vulnintel* 路由从 server 搬到插件；
//   - Step 4 批 4：vulnintel 抓取/富化管线搬入 plugins/intel/vulnintel，插件
//     Start/Stop 管理轮询生命周期，server.Manager 不再持有该管线；
//   - Step 4 批 5：vuln_kb / vuln_intel SQL 搬入 plugins/intel/vulnstore，domain
//     只保留类型，db 只保留 VulnKBStore 注入槽位；
//   - 表归属仍以 core/ownership 为准（plugin-intel 四张表）。
//
// 插件不 import finduai/db（分层红线）：查询通过 Deps.Store，写入通过
// vulnintel/vulnstore 的接口，由装配层注入。
package intel

import (
	"context"
	_ "embed"
	"errors"
	"sync/atomic"
	"time"

	dfe "finduai/domain/finding"

	"finduai/core/pluginhost"
)

//go:embed plugin.json
var manifestJSON []byte

// Store 是宿主注入的只读存储。vulnstore.Store 实现该接口；插件只拿到行为，
// 不 import finduai/db（layercheck 禁止 plugins -> db）。
type Store interface {
	ListVulnKB(q, vulnType string, page, size int) ([]dfe.VulnKBEntry, int, error)
	GetVulnKB(id int64) (*dfe.VulnKBEntry, error)
	VulnKBStats() (*dfe.VulnKBStats, error)
	ListVulnIntel(q, severity string, page, size int) ([]dfe.EnrichedVulnIntel, int, error)
}

// IntelPipeline 是漏洞情报抓取/富化管线的生命周期接口。具体实现位于
// plugins/intel/vulnintel，由宿主装配时注入；插件测试可用 fake 实现。
type IntelPipeline interface {
	Start()
	Stop()
	Refresh()
	Status() (polling bool, last time.Time, result any, sources int)
}

// Deps 是装配层注入的依赖。Bootstrap 用闭包传入，避免插件反向依赖
// vulnkb / fingerprint（它们仍需要宿主能力，会破坏分层）。
type Deps struct {
	Store Store
	// Bootstrap 在首次 Start 时执行一次：指纹规则库建表/同步 + 1day 漏洞库导入。
	Bootstrap func()
	// Intel 为漏洞情报管线；nil 时 refresh/status 返回 503。
	Intel IntelPipeline
}

// Plugin 实现 pluginhost.Plugin 与 pluginhost.RouteProvider。
type Plugin struct {
	manifest     pluginhost.Manifest
	deps         Deps
	started      atomic.Bool
	bootstrapped atomic.Bool
}

// New 创建插件并严格解析内嵌清单。Store 必须注入，否则视为装配错误。
func New(deps Deps) (*Plugin, error) {
	if deps.Store == nil {
		return nil, errors.New("intel: 未注入 Store")
	}
	m, err := pluginhost.ParseManifest(manifestJSON)
	if err != nil {
		return nil, err
	}
	return &Plugin{manifest: m, deps: deps}, nil
}

// Manifest 返回不可变清单。
func (p *Plugin) Manifest() pluginhost.Manifest { return p.manifest }

// Start 标记插件可用，并在首次启动时触发一次漏洞库后台导入（幂等）。
// 重复 Start 由 pluginhost 拒绝；Stop 后再 Start 不会重复导入。
func (p *Plugin) Start(context.Context) error {
	p.started.Store(true)
	if p.bootstrapped.CompareAndSwap(false, true) && p.deps.Bootstrap != nil {
		p.deps.Bootstrap()
	}
	if p.deps.Intel != nil {
		p.deps.Intel.Start()
	}
	return nil
}

// Stop 关闭入口并停止情报轮询/富化循环；1day 导入是幂等的，不强行中断。
func (p *Plugin) Stop(context.Context) error {
	p.started.Store(false)
	if p.deps.Intel != nil {
		p.deps.Intel.Stop()
	}
	return nil
}

// Health 在停止状态下返回 ErrInvalidState（pluginhost 通常不会在此状态调用）。
func (p *Plugin) Health(context.Context) error {
	if !p.started.Load() {
		return pluginhost.ErrInvalidState
	}
	return nil
}

// Routes 返回本插件贡献的六条路由。
func (p *Plugin) Routes() []pluginhost.Route {
	return []pluginhost.Route{
		{Pattern: "GET /api/vulnkb", Handler: p.listVulnKB},
		{Pattern: "GET /api/vulnkb/stats", Handler: p.vulnKBStats},
		{Pattern: "GET /api/vulnkb/{id}", Handler: p.getVulnKB},
		{Pattern: "GET /api/vulnintel", Handler: p.listVulnIntel},
		{Pattern: "POST /api/vulnintel/refresh", Handler: p.refreshVulnIntel},
		{Pattern: "GET /api/vulnintel/status", Handler: p.vulnIntelStatus},
	}
}
