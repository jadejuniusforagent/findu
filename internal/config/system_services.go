package config

// SystemServicesConfig 定义本机（WSL/宿主机）"运行服务"注册表。
// 按"模块"对服务进行分组统计，供 WebUI「运行服务」页面展示状态并支持按需启停、
// 开机自启（systemd enable/disable）。
//
// 设计目标：把重启后不会自动拉起的本机服务（如 Ollama、平台主进程、代理/隧道等）
// 集中登记，前端按模块分组呈现，并允许管理员在 UI 上按需启用。
type SystemServicesConfig struct {
	// Enabled 是否启用该功能；为 false 时 API 返回 enabled=false 且不展示服务。
	Enabled bool `yaml:"enabled,omitempty" json:"enabled"`
	// Modules 服务模块列表（按功能模块分组，如 AI/知识库、核心服务、网络代理等）。
	Modules []SystemServiceModule `yaml:"modules,omitempty" json:"modules"`
}

// SystemServiceModule 一个功能模块，包含若干服务。
type SystemServiceModule struct {
	ID          string                `yaml:"id" json:"id"`
	Name        string                `yaml:"name" json:"name"`
	Description string                `yaml:"description,omitempty" json:"description,omitempty"`
	Services    []SystemServiceConfig `yaml:"services" json:"services"`
}

// SystemServiceConfig 单个服务定义。
//
// Kind 取值：
//   - systemd : 由 systemd 管理，探测 `systemctl is-active <unit>`，可 start/stop/restart/enable/disable
//   - process : 由命令行启动，探测 `pgrep -f <match>`（可选再校验端口），可用 start_cmd/stop_cmd 控制
//   - port    : 仅探测 TCP 端口 127.0.0.1:<port>，可选 start_cmd/stop_cmd
//   - http    : 仅探测 HTTP GET <url>，可选 start_cmd/stop_cmd
type SystemServiceConfig struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Kind        string `yaml:"kind" json:"kind"`

	// systemd
	Unit string `yaml:"unit,omitempty" json:"unit,omitempty"`

	// process
	Match   string `yaml:"match,omitempty" json:"match,omitempty"`     // pgrep -f 匹配串
	WorkDir string `yaml:"workdir,omitempty" json:"workdir,omitempty"` // 启动命令的工作目录
	LogFile string `yaml:"log_file,omitempty" json:"log_file,omitempty"`

	// port / http
	Port int    `yaml:"port,omitempty" json:"port,omitempty"`
	URL  string `yaml:"url,omitempty" json:"url,omitempty"`

	// Host 仅 kind=port 使用：探测的 TCP 主机，默认 127.0.0.1。
	// 设为容器 DNS 名（如 postgres、findu-ollama）即可做容器化探测。
	Host string `yaml:"host,omitempty" json:"host,omitempty"`

	// 控制命令（process/port/http 可选；systemd 使用 systemctl）
	StartCmd string `yaml:"start_cmd,omitempty" json:"start_cmd,omitempty"`
	StopCmd  string `yaml:"stop_cmd,omitempty" json:"stop_cmd,omitempty"`

	// Autostart 期望的开机自启状态，用于在 UI 上提示"期望自启但未启用"。
	Autostart bool `yaml:"autostart,omitempty" json:"autostart"`
	// Optional 标记为可选服务（缺失时不计入"异常"统计）。
	Optional bool `yaml:"optional,omitempty" json:"optional"`
}
