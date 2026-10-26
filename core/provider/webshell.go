package provider

import (
	"context"
	"time"
)

// ConnSpec 描述一条 WebShell 连接。
type ConnSpec struct {
	URL      string            `json:"url"`
	Method   string            `json:"method,omitempty"`
	Password string            `json:"password,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Timeout  time.Duration     `json:"timeout,omitempty"`
}

// Conn 是已建立连接的快照。
type Conn struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Status    string    `json:"status"` // open / closed
	CreatedAt time.Time `json:"created_at"`
}

// Health 是连接探活结果。
type Health struct {
	OK      bool          `json:"ok"`
	Latency time.Duration `json:"latency"`
	Message string        `json:"message,omitempty"`
}

// ExecResult 是命令执行结果。
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// FileEntry 是目录项。
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// WebShellProvider 是 WebShell 能力接口（contracts/capability-providers.md §三）。
// 实现自持 webshell_connections / webshell_connection_states 两张表。
type WebShellProvider interface {
	Open(ctx context.Context, spec ConnSpec) (Conn, error)
	Close(ctx context.Context, id string) error
	Test(ctx context.Context, id string) (Health, error)

	Exec(ctx context.Context, id, command string) (ExecResult, error)

	ListFiles(ctx context.Context, id, path string) ([]FileEntry, error)
	ReadFile(ctx context.Context, id, path string) ([]byte, error)
	WriteFile(ctx context.Context, id, path string, content []byte) error
	DeleteFile(ctx context.Context, id, path string) error
	Upload(ctx context.Context, id, path string, content []byte) error
}
