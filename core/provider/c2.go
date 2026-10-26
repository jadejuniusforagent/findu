package provider

import (
	"context"
	"time"
)

// C2 事件名。插件按这些名字发事件，宿主按名字订阅，双方不共享结构体。
const (
	EventC2ListenerStarted = "c2.listener.started"
	EventC2ListenerStopped = "c2.listener.stopped"
	EventC2SessionOpened   = "c2.session.opened"
	EventC2SessionClosed   = "c2.session.closed"
	EventC2TaskDispatched  = "c2.task.dispatched"
	EventC2TaskCompleted   = "c2.task.completed"
)

// ListenerSpec 描述一个待启动的监听器。
type ListenerSpec struct {
	Name     string         `json:"name"`
	Address  string         `json:"address"`
	Protocol string         `json:"protocol"`
	Options  map[string]any `json:"options,omitempty"`
}

// Listener 是已启动监听器的状态快照。
type Listener struct {
	ID        string       `json:"id"`
	Spec      ListenerSpec `json:"spec"`
	Status    string       `json:"status"` // running / stopped
	CreatedAt time.Time    `json:"created_at"`
}

// Session 是一条 C2 会话。
type Session struct {
	ID         string    `json:"id"`
	ListenerID string    `json:"listener_id"`
	Host       string    `json:"host"`
	User       string    `json:"user"`
	PID        int       `json:"pid"`
	Status     string    `json:"status"` // active / closed
	LastSeen   time.Time `json:"last_seen"`
}

// SessionFilter 用于列会话。零值表示不过滤、不限量。
type SessionFilter struct {
	ListenerID string `json:"listener_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// TaskState 是 C2 任务状态。
type TaskState string

const (
	TaskPending   TaskState = "pending"
	TaskRunning   TaskState = "running"
	TaskCompleted TaskState = "completed"
	TaskFailed    TaskState = "failed"
	TaskKilled    TaskState = "killed"
)

// TaskSpec 描述一条下发到会话的命令。
type TaskSpec struct {
	SessionID string         `json:"session_id"`
	Command   string         `json:"command"`
	Timeout   time.Duration  `json:"timeout,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// Task 是任务状态快照。
type Task struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	State     TaskState `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskResult 是任务执行结果。
type TaskResult struct {
	TaskID   string `json:"task_id"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// PayloadSpec 描述待生成的 payload。
type PayloadSpec struct {
	Name    string         `json:"name"`
	Format  string         `json:"format"`
	Host    string         `json:"host,omitempty"`
	Port    int            `json:"port,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// Artifact 是已生成的 payload 文件。
type Artifact struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// C2Provider 是 C2 能力接口（contracts/capability-providers.md §二）。
// 实现自持 c2_* 七张表；宿主不直接读写这些表。
type C2Provider interface {
	StartListener(ctx context.Context, spec ListenerSpec) (Listener, error)
	StopListener(ctx context.Context, id string) error
	ListListeners(ctx context.Context) ([]Listener, error)

	ListSessions(ctx context.Context, filter SessionFilter) ([]Session, error)
	GetSession(ctx context.Context, id string) (Session, error)
	KillSession(ctx context.Context, id string) error

	Dispatch(ctx context.Context, spec TaskSpec) (Task, error)
	PollTask(ctx context.Context, id string) (Task, error)
	TaskResult(ctx context.Context, id string) (TaskResult, error)

	BuildPayload(ctx context.Context, spec PayloadSpec) (Artifact, error)
	ListPayloads(ctx context.Context) ([]Artifact, error)

	// Events 返回 fromSeq 之后的事件流，实现负责关闭 channel。
	Events(ctx context.Context, fromSeq int64) (<-chan Event, error)
}
