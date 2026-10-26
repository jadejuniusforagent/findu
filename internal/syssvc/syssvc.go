// Package syssvc 提供本机（WSL/宿主机）"运行服务"注册表：按模块对服务进行分组，
// 探测运行状态，并支持按需启停与开机自启（systemd enable/disable）。
//
// 该包被 WebUI「运行服务」页面使用：把重启后不会自动拉起的服务集中登记，
// 前端按模块分组统计并允许管理员在 UI 上按需启用。
package syssvc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"finduai/internal/config"
	"go.uber.org/zap"
)

// 服务类型
const (
	KindSystemd = "systemd"
	KindProcess = "process"
	KindPort    = "port"
	KindHTTP    = "http"
)

// 状态取值
const (
	StateRunning = "running"
	StateStopped = "stopped"
	StateUnknown = "unknown"
)

const defaultCmdTimeout = 20 * time.Second

// Registry 服务注册表。
type Registry struct {
	enabled bool
	modules []config.SystemServiceModule
	logger  *zap.Logger
}

// NewRegistry 由配置构建注册表。
func NewRegistry(cfg config.SystemServicesConfig, logger *zap.Logger) *Registry {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Registry{
		enabled: cfg.Enabled,
		modules: cfg.Modules,
		logger:  logger,
	}
}

// Enabled 返回功能是否启用。
func (r *Registry) Enabled() bool { return r != nil && r.enabled }

// ServiceStatus 单个服务的探测结果。
type ServiceStatus struct {
	ModuleID    string `json:"module_id"`
	ModuleName  string `json:"module_name"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`

	Running bool   `json:"running"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`

	ExpectedAutostart  bool `json:"expected_autostart"`
	AutostartEnabled   bool `json:"autostart_enabled"`
	AutostartSupported bool `json:"autostart_supported"`
	StartSupported     bool `json:"start_supported"`
	StopSupported      bool `json:"stop_supported"`
	Optional           bool `json:"optional"`

	SuggestedUnit string `json:"suggested_unit,omitempty"`
	Error         string `json:"error,omitempty"`
}

// ModuleStatus 单个模块的聚合结果（含统计）。
type ModuleStatus struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Services    []ServiceStatus `json:"services"`
	Total       int             `json:"total"`
	Running     int             `json:"running"`
	Stopped     int             `json:"stopped"`
	AutostartOK int             `json:"autostart_ok"`
}

// Snapshot 全量快照（含总统计）。
type Snapshot struct {
	Enabled     bool           `json:"enabled"`
	Modules     []ModuleStatus `json:"modules"`
	Total       int            `json:"total"`
	Running     int            `json:"running"`
	Stopped     int            `json:"stopped"`
	AutostartOK int            `json:"autostart_ok"`
	GeneratedAt time.Time      `json:"generated_at"`
}

// find 按服务 ID 查找服务定义。
func (r *Registry) find(id string) (*config.SystemServiceConfig, *config.SystemServiceModule, error) {
	if r == nil {
		return nil, nil, fmt.Errorf("服务注册表不可用")
	}
	for mi := range r.modules {
		m := &r.modules[mi]
		for si := range m.Services {
			if m.Services[si].ID == id {
				return &m.Services[si], m, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("未找到服务: %s", id)
}

// Snapshot 探测所有服务并返回按模块聚合的快照。
func (r *Registry) Snapshot(ctx context.Context) Snapshot {
	snap := Snapshot{Enabled: r.Enabled(), GeneratedAt: time.Now(), Modules: []ModuleStatus{}}
	if !snap.Enabled {
		return snap
	}
	for mi := range r.modules {
		m := &r.modules[mi]
		ms := ModuleStatus{ID: m.ID, Name: m.Name, Description: m.Description, Services: []ServiceStatus{}}
		for si := range m.Services {
			st := r.probe(ctx, &m.Services[si], m)
			ms.Services = append(ms.Services, st)
			ms.Total++
			snap.Total++
			if st.Running {
				ms.Running++
				snap.Running++
			} else {
				ms.Stopped++
				snap.Stopped++
			}
			if st.AutostartSupported && st.AutostartEnabled {
				ms.AutostartOK++
				snap.AutostartOK++
			}
		}
		snap.Modules = append(snap.Modules, ms)
	}
	return snap
}

// probe 探测单个服务状态。
func (r *Registry) probe(ctx context.Context, s *config.SystemServiceConfig, m *config.SystemServiceModule) ServiceStatus {
	st := ServiceStatus{
		ModuleID:          m.ID,
		ModuleName:        m.Name,
		ID:                s.ID,
		Name:              s.Name,
		Description:       s.Description,
		Kind:              s.Kind,
		ExpectedAutostart: s.Autostart,
		Optional:          s.Optional,
		State:             StateUnknown,
	}

	switch strings.ToLower(strings.TrimSpace(s.Kind)) {
	case KindSystemd:
		st.AutostartSupported = true
		st.StartSupported = true
		st.StopSupported = true
		active := r.systemctl(ctx, "is-active", s.Unit)
		enabled := r.systemctl(ctx, "is-enabled", s.Unit)
		st.AutostartEnabled = enabled == "enabled" || enabled == "enabled-runtime"
		st.Running = active == "active" || active == "activating"
		if st.Running {
			st.State = StateRunning
		} else {
			st.State = StateStopped
		}
		st.Detail = fmt.Sprintf("systemd unit=%s active=%s enabled=%s", s.Unit, orDash(active), orDash(enabled))
	case KindProcess:
		st.StartSupported = strings.TrimSpace(s.StartCmd) != ""
		st.StopSupported = true // 至少可用 pkill，若有 stop_cmd 更精确
		st.SuggestedUnit = suggestedUnit(s)
		matched := r.pgrep(ctx, s.Match)
		portOK := true
		if s.Port > 0 {
			portOK = tcpOpen(ctx, "127.0.0.1", s.Port)
		}
		st.Running = matched != "" && portOK
		if st.Running {
			st.State = StateRunning
		} else {
			st.State = StateStopped
		}
		detail := fmt.Sprintf("pid=[%s]", matched)
		if s.Port > 0 {
			detail += fmt.Sprintf(" port=%d up=%v", s.Port, portOK)
		}
		st.Detail = detail
	case KindPort:
		st.StartSupported = strings.TrimSpace(s.StartCmd) != ""
		st.StopSupported = strings.TrimSpace(s.StopCmd) != ""
		host := strings.TrimSpace(s.Host)
		if host == "" {
			host = "127.0.0.1"
		}
		st.Running = tcpOpen(ctx, host, s.Port)
		if st.Running {
			st.State = StateRunning
		} else {
			st.State = StateStopped
		}
		st.Detail = fmt.Sprintf("tcp %s:%d", host, s.Port)
	case KindHTTP:
		st.StartSupported = strings.TrimSpace(s.StartCmd) != ""
		st.StopSupported = strings.TrimSpace(s.StopCmd) != ""
		code, err := httpProbe(ctx, s.URL)
		if err != nil {
			st.Running = false
			st.State = StateStopped
			st.Detail = fmt.Sprintf("http %s -> %v", s.URL, err)
		} else {
			st.Running = true
			st.State = StateRunning
			st.Detail = fmt.Sprintf("http %s -> %d", s.URL, code)
		}
	default:
		st.Error = fmt.Sprintf("未知服务类型: %s", s.Kind)
	}
	return st
}

// systemctl 执行 systemctl <verb> <unit> 并返回首行输出（忽略非零退出码）。
func (r *Registry) systemctl(ctx context.Context, verb, unit string) string {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return ""
	}
	out, _ := runCmd(ctx, "", "systemctl", verb, unit)
	if out == "" {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
}

// pgrep 返回匹配进程的 pid 列表（逗号分隔）。
func (r *Registry) pgrep(ctx context.Context, match string) string {
	match = strings.TrimSpace(match)
	if match == "" {
		return ""
	}
	out, _ := runCmd(ctx, "", "pgrep", "-f", match)
	if out == "" {
		return ""
	}
	var pids []string
	for _, line := range strings.Split(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			pids = append(pids, p)
		}
	}
	return strings.Join(pids, ",")
}

// Start 启动服务。
func (r *Registry) Start(ctx context.Context, id string) error {
	s, _, err := r.find(id)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(s.Kind)) {
	case KindSystemd:
		return r.systemctlRun(ctx, "start", s.Unit)
	default:
		if strings.TrimSpace(s.StartCmd) == "" {
			return fmt.Errorf("服务 %s 未配置启动命令", id)
		}
		return startDetached(s.StartCmd, s.WorkDir, s.LogFile)
	}
}

// Stop 停止服务。
func (r *Registry) Stop(ctx context.Context, id string) error {
	s, _, err := r.find(id)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(s.Kind)) {
	case KindSystemd:
		return r.systemctlRun(ctx, "stop", s.Unit)
	default:
		if strings.TrimSpace(s.StopCmd) != "" {
			out, err := runCmd(ctx, s.WorkDir, "bash", "-lc", s.StopCmd)
			if err != nil {
				return fmt.Errorf("停止失败: %v (%s)", err, out)
			}
			return nil
		}
		if strings.TrimSpace(s.Match) != "" {
			_, _ = runCmd(ctx, "", "pkill", "-f", s.Match)
			return nil
		}
		return fmt.Errorf("服务 %s 未配置停止命令", id)
	}
}

// Restart 重启服务。
func (r *Registry) Restart(ctx context.Context, id string) error {
	s, _, err := r.find(id)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(s.Kind)) == KindSystemd {
		return r.systemctlRun(ctx, "restart", s.Unit)
	}
	if err := r.Stop(ctx, id); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	return r.Start(ctx, id)
}

// SetAutostart 开启/关闭开机自启（仅 systemd 支持）。
func (r *Registry) SetAutostart(ctx context.Context, id string, enable bool) error {
	s, _, err := r.find(id)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(s.Kind)) != KindSystemd {
		return fmt.Errorf("服务 %s 非 systemd 类型，无法设置开机自启（可参考 suggested_unit 手动配置）", id)
	}
	verb := "disable"
	if enable {
		verb = "enable"
	}
	return r.systemctlRun(ctx, verb, s.Unit)
}

// Status 返回单个服务的最新状态。
func (r *Registry) Status(ctx context.Context, id string) (ServiceStatus, error) {
	s, m, err := r.find(id)
	if err != nil {
		return ServiceStatus{}, err
	}
	return r.probe(ctx, s, m), nil
}

func (r *Registry) systemctlRun(ctx context.Context, verb, unit string) error {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return fmt.Errorf("systemd unit 为空")
	}
	cctx, cancel := context.WithTimeout(ctx, defaultCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "systemctl", verb, unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s %s 失败: %v (%s)", verb, unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runCmd 执行命令并返回合并输出。
func runCmd(ctx context.Context, workdir string, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, defaultCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// startDetached 以独立会话启动命令，确保脱离服务进程可继续存活。
func startDetached(command, workdir, logFile string) error {
	cmd := exec.Command("setsid", "bash", "-lc", command)
	if workdir != "" {
		cmd.Dir = workdir
	}
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			cmd.Stdout = f
			cmd.Stderr = f
		}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	// 交给 init 托管，避免僵尸进程
	_ = cmd.Process.Release()
	return nil
}

// tcpOpen 探测主机端口是否可连接。
func tcpOpen(ctx context.Context, host string, port int) bool {
	if port <= 0 {
		return false
	}
	if host == "" {
		host = "127.0.0.1"
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// httpProbe 探测 HTTP 服务。
func httpProbe(ctx context.Context, url string) (int, error) {
	if strings.TrimSpace(url) == "" {
		return 0, fmt.Errorf("url 为空")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func suggestedUnit(s *config.SystemServiceConfig) string {
	if strings.TrimSpace(s.StartCmd) == "" {
		return ""
	}
	wd := s.WorkDir
	if wd == "" {
		wd = "/opt"
	}
	name := s.ID
	if name == "" {
		name = "service"
	}
	return fmt.Sprintf(`[Unit]
Description=%s
After=network.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=/bin/bash -lc '%s'
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target`, orDash(s.Name), wd, s.StartCmd)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
