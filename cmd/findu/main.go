// Command findu runs the FINDU backend: the dual SQLite graph stores,
// the event-driven exploration engine, and the JSON HTTP API consumed by the
// shadcn/ui frontend.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"finduai/agent"
	"finduai/core/config"
	"finduai/db"
	"finduai/finduapp"
	"finduai/internal/security"
	"finduai/selfupdate"
	"finduai/server"
)

// seedFinduToolCatalog 把 findu 子系统的 android_*/miniprogram_* MCP 工具以
// runtime='findu' 幂等 seed 进统一 tools 表，并默认绑定渗透/审计/默认三角色
// （与既有 findu 工具行一致，见 migrations/2026-10-18-tools-unify.sql）。角色
// 工具绑定经 boundFinduToolsNative 按 runtime='findu' 过滤，入表后角色页即可
// 绑定。其他 findu 运行时工具不入表（行为保持原样）。
func seedFinduToolCatalog(pg *db.DB, sub *finduapp.Subsystem) {
	if pg == nil {
		return
	}
	tools := sub.ToolSeeds()
	if len(tools) == 0 {
		return
	}
	bindings, _ := json.Marshal([]string{"findu_default", "findu_audit", "pentest"})
	seeded := 0
	for _, t := range tools {
		if !strings.HasPrefix(t.Name, "android_") && !strings.HasPrefix(t.Name, "miniprogram_") {
			continue
		}
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			log.Printf("[tools] seed %s schema 序列化失败: %v", t.Name, err)
			continue
		}
		if err := pg.SeedFinduTool(t.Name, t.Description, schema, bindings); err != nil {
			log.Printf("[tools] seed %s 失败: %v", t.Name, err)
			continue
		}
		seeded++
	}
	log.Printf("[tools] findu 工具 seed 完成: %d 个 android_*/miniprogram_* 入表（runtime=findu，默认绑定 pentest/findu_default/findu_audit）", seeded)
}

// version is the build version, injected at release time via
// -ldflags "-X main.version=<tag>". Defaults to "dev" for local builds.
var version = "dev"

const banner = `
  FindUAI — 自主渗透测试控制台
`

// printBanner writes the startup banner + version/runtime info to stdout.
func printBanner(addr string) {
	fmt.Print(banner)
	fmt.Printf("  版本 %s  ·  %s/%s  ·  %s  ·  监听 %s\n\n",
		version, runtime.GOOS, runtime.GOARCH, runtime.Version(), addr)
}

// main only maps run's result onto the process exit code. The exit code is part
// of the update protocol — the supervising start script reads it to decide
// whether to relaunch us (see selfupdate.ExitRestart) — so the body has to live
// in a function that can *return* rather than os.Exit past its own defers.
func main() {
	os.Exit(run())
}

func run() int {
	var (
		addr    = flag.String("addr", ":8787", "HTTP listen address")
		dataDir = flag.String("data", filepath.Join(config.BaseDir(), "data"), "data directory for SQLite stores (default: data/ next to the executable)")
		proxy   = flag.String("proxy", "127.0.0.1:8788", "traffic recording proxy address (empty to disable)")
	)
	flag.Parse()

	// hand the build version to the server package so GET /api/health can report it
	// to the frontend top bar.
	server.BuildVersion = version

	printBanner(*addr)

	// capture backend logs into the in-memory sink (still to stderr) so the /logs
	// page can show a live log stream. Do this first, to catch startup logs too.
	server.StartLogCapture()

	// Self-update bootstrap: swap in a staged binary, or count a post-swap boot
	// attempt and roll back if the new build keeps dying. Must run before we open
	// the stores or bind a port — this may end with "exit and let the start script
	// relaunch me", and there is no point paying for either first.
	action, upState := selfupdate.Bootstrap()
	server.SetBootUpdateState(upState)
	if action == selfupdate.Restart {
		return selfupdate.ExitRestart
	}

	// surface which config file the binary reads (absolute, so `go run`'s relative
	// "config.json" — resolved against the CWD — is unambiguous).
	cfgPath := config.Path()
	if abs, e := filepath.Abs(cfgPath); e == nil {
		cfgPath = abs
	}
	if _, e := os.Stat(cfgPath); e == nil {
		log.Printf("[config] 配置文件: %s", cfgPath)
	} else {
		log.Printf("[config] 配置文件: %s (不存在 — 将仅尝试环境变量 FINDU_PG_DSN)", cfgPath)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, shutdown := shutdownContext(sigCtx)
	defer shutdown(agent.AbortShutdown)

	mgr, err := server.NewManager(*dataDir, *proxy)
	if err != nil {
		log.Fatalf("open stores: %v", err)
	}
	defer mgr.Close()

	// Surviving this long means a freshly swapped-in build actually works, so drop
	// the upgrade marker and stop counting attempts. Until it fires, every boot
	// increments the count and a build that keeps dying gets rolled back.
	settle := time.AfterFunc(selfupdate.SettleDelay, selfupdate.Settle)
	defer settle.Stop()

	skillDir := config.SkillDir()
	if abs, err := filepath.Abs(skillDir); err == nil {
		skillDir = abs
	}
	log.Printf("[config] skill 目录: %s", skillDir)
	// JWT 签名密钥要同时满足两点：①跨容器重建持久（rootfs 会被重造）；②不在可浏览的工作区里
	// ——dataDir 是 /api/workspace/{list,read,download} 的根，密钥放进去等于对外公开。
	// 因此默认放在 dataDir 的同级目录 keys/（容器内 /app/keys，单独挂卷），可用
	// FINDU_KEY_DIR 覆盖。
	keyDir := config.LoadSettings().KeyDir
	if keyDir == "" {
		keyDir = filepath.Join(filepath.Dir(*dataDir), "keys")
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		log.Printf("[auth] 创建密钥目录失败: %v", err)
	}
	log.Printf("[auth] JWT 密钥目录: %s", keyDir)
	srv := server.New(ctx, mgr, skillDir, *dataDir, keyDir)

	// 账号统一到 findu：让 findu 子系统用控制台的 findu_token cookie 认证，
	// findu 自己的密码登录已停用（/findu/api/auth/login → 410）。
	security.SetFinduTokenVerifier(srv.FinduTokenSubject)

	// findu 的 YAML 工具配方（nmap / nuclei / ffuf / sqlmap …）接入工具层。
	// 与 /findu/ 挂载开关无关：只读 findu 的配置文件，用来把配方变成 CoreTool。
	if rtools, rnames, rerr := finduapp.RecipeTools(); rerr != nil {
		log.Printf("[recipes] findu 配方未接入: %v", rerr)
	} else if len(rtools) > 0 {
		srv.SetRecipeTools(rtools, rnames)
	}

	// 逐目标门禁资源装配（guard 的 scope/限速/side_effect 影子门禁）：
	// 工具索引 = 内置 + hostTools union（含配方），切换开关见 settings
	// guard_scope_enforce / guard_rate_limit_per_host（默认影子模式）。
	srv.WireGuardPolicy()

	// FindUAI 子系统（迁移期并存）：FINDU_ENABLE=1 时把原 findu 的完整服务挂到 /findu/。
	hostHandler := srv.Handler()
	if finduapp.Enabled() {
		var model *finduapp.ModelConfig
		if p, err := mgr.PG().ActiveProfile(); err != nil {
			log.Printf("[findu] 读取活动 LLM profile 失败: %v", err)
		} else if p != nil {
			model = &finduapp.ModelConfig{
				Provider: p.Format,
				APIKey:   p.APIKey,
				BaseURL:  p.BaseURL,
				Model:    p.Model,
			}
			log.Printf("[findu] 多代理模型使用 findu 活动 LLM profile #%d (%s)", p.ID, p.Model)
		}
		if sub, err := finduapp.MountWithModel(model); err != nil {
			log.Printf("[findu] 子系统未启用: %v", err)
		} else {
			defer sub.Shutdown()
			// 多代理路由由宿主接管；deep / plan_execute / supervisor 继续复用
			// 现有多代理运行时，保留原有 SSE、任务板和深挖行为。
			srv.SetMultiAgentAPI(sub.MultiAgentHandler())
			// Step 6d 资源兼容入口：宿主拥有 /api/assets、/api/audit 路由，
			// 暂时复用 findu handler，直到业务/数据完全迁出。
			srv.SetFinduCompatAPI(sub.Handler)
			// Step 6d Phase R 运行时收口：closure 域（hitl decision/dismiss、
			// agent-loop、batch-tasks、external-mcp、webshell/exec）由宿主持有，
			// 进程内引用 findu 运行态 handler 实例。
			srv.SetFinduRuntime(sub.RuntimeHandles())
			// 工具绑定闭环（2026-10-22）：findu 子系统的 android_*/miniprogram_* MCP
			// 工具此前仅运行时注册、无表行，角色页无法绑定。以 runtime='findu' 幂等
			// seed 进统一 tools 表（首插生效，UI 编辑不被覆盖），默认绑定三角色。
			seedFinduToolCatalog(mgr.PG(), sub)
			// 合并挂载：/api 只留一个命名空间 —— 宿主优先，findu 兜底，
			// findu 的资源前缀（OwnedAPIPaths）直接交给 findu。旧 UI 仍在 /findu/。
			hostHandler = finduapp.Install(srv.HandlerWithFallback(sub.Handler, finduapp.OwnedAPIPaths()), sub)
			log.Printf("[findu] API 已并入 /api 命名空间（兜底 + %d 个资源前缀优先），多代理路由由宿主接管，旧 UI 保留在 %s/", len(finduapp.OwnedAPIPaths()), finduapp.Prefix)
		}
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           hostHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("FindUAI %s backend listening on %s (data=%s, workers=%d)", version, *addr, *dataDir, mgr.Workers())
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	// Two ways out: a signal (normal stop → exit 0, the start script stops looping)
	// or a staged update / rollback (→ exit 75, the script relaunches us and the
	// bootstrap above installs the new build).
	code := 0
	select {
	case <-ctx.Done():
	case <-server.RestartRequested():
		code = selfupdate.ExitRestart
		shutdown(agent.AbortShutdown)
	}

	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	return code
}

// shutdownContext deliberately does not derive from signalCtx. If it did, the
// parent's plain context.Canceled could win the race before AbortShutdown was
// attached to the child, losing the diagnostic cause in every running Agent.
func shutdownContext(signalCtx context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, shutdown := context.WithCancelCause(context.Background())
	go func() {
		select {
		case <-signalCtx.Done():
			shutdown(agent.AbortShutdown)
		case <-ctx.Done():
		}
	}()
	return ctx, shutdown
}
