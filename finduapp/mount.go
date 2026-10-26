// Package finduapp 把 findu 的完整服务挂载到底座的
// HTTP mux 上，用于迁移期的并存运行（Strangler 模式）。由环境变量 FINDU_ENABLE=1 启用。
//
// 挂载形态：
//
//	/findu/...   → findu 的 gin 路由（页面 + /api，经 StripPrefix 去掉前缀）
//	页面 HTML 中的 /static/ 绝对引用在响应流里改写为 /findu/static/
//	运行时新增的 fetch / XHR / DOM 引用由注入的 shim 改写
//
// findu 自身的地基（Gin 路由、SQLite、自己的 RBAC）在此阶段保持不变，
// 以便与底座的 PG / net/http / session 并存；收敛在 P3~P5 逐步进行。
package finduapp

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	corecfg "finduai/core/config"
	"finduai/internal/app"
	"finduai/internal/config"
	"finduai/internal/logger"
	"finduai/internal/mcp"
)

// Prefix 是 findu 子系统在宿主上的挂载前缀。
const Prefix = "/findu"

// Subsystem 持有已初始化的 findu 应用。
type Subsystem struct {
	Handler http.Handler
	app     *app.App
}

// Enabled 由 FINDU_ENABLE=1 开启，统一由 core/config 解析。
func Enabled() bool {
	return corecfg.LoadSettings().FinduEnable
}

// configPath 优先 FINDU_CONFIG，默认仓库根的 config-findu.yaml。
func configPath() string {
	if p := corecfg.LoadSettings().FinduConfig; p != "" {
		return p
	}
	return "config-findu.yaml"
}

// ModelConfig is the host's active LLM profile, used to override findu's
// YAML/env model before the multi-agent runtime is constructed. findu's own
// FINDU_LLM_API_KEY may be unset in the embedded deployment, while the host
// already has a working DB-backed profile.
type ModelConfig struct {
	Provider string
	APIKey   string
	BaseURL  string
	Model    string
}

// Mount 初始化 findu 子系统；失败时返回 error 交给宿主决定是降级还是退出。
func Mount() (*Subsystem, error) {
	return MountWithModel(nil)
}

// MountWithModel mounts findu with an optional host LLM override.
func MountWithModel(model *ModelConfig) (*Subsystem, error) {
	cp := configPath()
	if abs, err := filepath.Abs(cp); err == nil {
		cp = abs
	}
	if _, err := os.Stat(cp); err != nil {
		return nil, fmt.Errorf("findu 配置文件不存在: %s", cp)
	}
	cfg, err := config.Load(cp)
	if err != nil {
		return nil, fmt.Errorf("加载 findu 配置 %s: %w", cp, err)
	}
	applyModelOverride(cfg, model)
	lg := logger.New("info", "stdout")
	a, err := app.New(cfg, lg, cp)
	if err != nil {
		return nil, fmt.Errorf("初始化 findu 应用: %w", err)
	}
	return &Subsystem{Handler: rewriteHTML(a.Handler()), app: a}, nil
}

// applyModelOverride replaces only the credential/endpoint fields, preserving
// findu's reasoning/token settings. The default AI channel is updated too so
// both cfg.OpenAI and ResolveAIChannel see the same working profile.
func applyModelOverride(cfg *config.Config, model *ModelConfig) {
	if cfg == nil || model == nil || strings.TrimSpace(model.APIKey) == "" {
		return
	}
	cfg.OpenAI.APIKey = strings.TrimSpace(model.APIKey)
	if v := strings.TrimSpace(model.BaseURL); v != "" {
		cfg.OpenAI.BaseURL = v
	}
	if v := strings.TrimSpace(model.Model); v != "" {
		cfg.OpenAI.Model = v
	}
	switch strings.ToLower(strings.TrimSpace(model.Provider)) {
	case "anthropic", "claude":
		cfg.OpenAI.Provider = "claude"
	case "openai", "openai-compatible", "openai_compatible", "":
		cfg.OpenAI.Provider = "openai"
	}
	def := config.NormalizeAIChannelID(cfg.AI.DefaultChannel)
	if def == "" {
		def = "default"
	}
	if cfg.AI.Channels == nil {
		cfg.AI.Channels = make(map[string]config.AIChannelConfig)
	}
	cfg.AI.Channels[def] = config.AIChannelFromOpenAI(def, "Default", cfg.OpenAI)
}

// Shutdown 关闭 findu 应用持有的资源。
func (s *Subsystem) Shutdown() {
	if s != nil && s.app != nil {
		s.app.Shutdown()
	}
}

// MultiAgentHandler exposes the findu multi-agent API for host-owned routing.// The multi-agent runtime (deep / plan_execute / supervisor) is retained as the
// implementation, while findu owns the /api/multi-agent route and auth boundary.
// The host's Bearer token is promoted to the findu_token cookie because findu's
// session bridge reads the cookie.
func (s *Subsystem) MultiAgentHandler() http.Handler {
	if s == nil || s.Handler == nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("findu_token"); err != nil {
			if h := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(h, "Bearer ") {
				rr := r.Clone(r.Context())
				rr.AddCookie(&http.Cookie{
					Name:  "findu_token",
					Value: strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")),
				})
				r = rr
			}
		}
		s.Handler.ServeHTTP(w, r)
	})
}

// RuntimeHandles 暴露 findu 运行态实例句柄（Step 6d Phase R 运行时收口桥，
// 宿主对 closure 域做进程内方法调用，不再经 compat 转发）。
func (s *Subsystem) RuntimeHandles() app.RuntimeHandles {
	if s == nil || s.app == nil {
		return app.RuntimeHandles{}
	}
	return s.app.RuntimeHandles()
}

// ToolSeeds 暴露 findu 子系统已注册的 MCP 工具定义（按 name 排序），供宿主
// seed 进统一 tools 表（runtime='findu'）——android_*/miniprogram_* 等运行时
// 注册工具借此获得表行，角色/技能绑定闭环可用。
func (s *Subsystem) ToolSeeds() []mcp.Tool {
	if s == nil || s.app == nil {
		return nil
	}
	return s.app.MCPToolSeeds()
}

// Install 把 findu 子系统挂到宿主 handler 上：/findu/* 走 findu，其余交还宿主。
func Install(host http.Handler, sub *Subsystem) http.Handler {
	mux := http.NewServeMux()
	// 旧 UI 页面退役（2026-10-23）：用户不再从 /findu/ 进入旧界面，首页重定向到
	// 新控制台 /。findu 引擎实例仍被宿主 /api 命名空间（HandlerWithFallback 兜底 /
	// finduCompat / finduRuntimeBridge / multi-agent）与 /findu/api（前端 finduFetch
	// 依赖前缀）引用，故引擎本身保留；仅 /findu 与 /findu/ 两个页面入口退役。
	strip := http.StripPrefix(Prefix, sub.Handler)
	mux.HandleFunc(Prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.HandleFunc(Prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, Prefix+"/") == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		strip.ServeHTTP(w, r)
	})
	mux.Handle("/", host)
	return mux
}

// htmlShim 在页面里把所有以 /api /static 开头的绝对请求改写到 /findu 前缀下。
// 目的：不改 findu 的 61 个 JS 文件，一处注入覆盖全部运行时引用（P5 前端收敛时移除）。
const htmlShim = `<script>
(function(){
  var P='/findu';
  function fix(u){
    if(typeof u!=='string') return u;
    if(u.indexOf(P+'/')===0) return u;
    // /api 已并入宿主命名空间（findu 兜底），不再改写；只保留 /static 前缀
    if(u.indexOf('/static/')===0) return P+u;
    return u;
  }
  var of=window.fetch;
  if(of){ window.fetch=function(u,o){ return of.call(this,fix(u),o); }; }
  try{
    var OX=window.XMLHttpRequest.prototype.open;
    window.XMLHttpRequest.prototype.open=function(m,u){ arguments[1]=fix(u); return OX.apply(this,arguments); };
  }catch(e){}
  try{
    var OS=window.WebSocket;
    if(OS){ window.WebSocket=function(u,p){ return new OS(fix(u),p); };
      window.WebSocket.prototype=OS.prototype; }
  }catch(e){}
  document.addEventListener('DOMContentLoaded',function(){
    var els=document.querySelectorAll('[href^="/static/"],[src^="/static/"],[action^="/api/"]');
    for(var i=0;i<els.length;i++){
      var el=els[i];
      if(el.hasAttribute('href')) el.setAttribute('href',fix(el.getAttribute('href')));
      if(el.hasAttribute('src')) el.setAttribute('src',fix(el.getAttribute('src')));
      if(el.hasAttribute('action')) el.setAttribute('action',fix(el.getAttribute('action')));
    }
  });
})();
</script>`

// rewriteHTML 在 HTML 响应里改写静态引用并注入 shim；非 HTML 原样透传。
func rewriteHTML(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 非页面请求直通：SSE/流式对话、静态资源、API 都必须保持原始 ResponseWriter，
		// 否则 Flusher/Hijacker 能力丢失会打断流式输出与 webshell 终端。
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/static/") ||
			strings.HasPrefix(p, "/mcp") || strings.HasPrefix(p, "/ws") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &bufferedResponse{header: make(http.Header)}
		next.ServeHTTP(rec, r)
		ctype := rec.header.Get("Content-Type")
		body := rec.body.Bytes()
		if strings.Contains(ctype, "text/html") || (len(body) > 15 && bytes.HasPrefix(bytes.ToLower(body), []byte("<!doctype")) || bytes.Contains(body[:min(len(body), 512)], []byte("<html"))) {
			s := string(body)
			s = strings.ReplaceAll(s, `href="/static/`, `href="`+Prefix+`/static/`)
			s = strings.ReplaceAll(s, `src="/static/`, `src="`+Prefix+`/static/`)
			s = strings.ReplaceAll(s, `"/static/`, `"`+Prefix+`/static/`)
			if i := strings.Index(strings.ToLower(s), "</head>"); i >= 0 {
				s = s[:i] + htmlShim + s[i:]
			} else if i := strings.Index(strings.ToLower(s), "<body"); i >= 0 {
				s = s[:i] + htmlShim + s[i:]
			} else {
				s = htmlShim + s
			}
			body = []byte(s)
			rec.header.Del("Content-Length")
			rec.header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
		}
		for k, vs := range rec.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.status)
		_, _ = w.Write(body)
	})
}

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

var _ io.Writer = (*bufferedResponse)(nil)
