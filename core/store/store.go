// Package store 定义 canonical 五实体的仓储接口。
//
// 设计约束（见 contracts/storage-contract.md）：
//
//	1 所有 runtime adapter 与插件只能通过这些接口落库，不得自建会话/消息类表
//	2 事务边界由 core 控制，仓储实现不内嵌跨表业务事务
//	3 分页统一 (limit, cursor)，不使用 offset
package store

import (
	"context"
	"errors"
	"time"
)

// 可判定的仓储错误。调用方用 errors.Is 判断，不要比对字符串。
var (
	ErrNotFound  = errors.New("store: not found")
	ErrConflict  = errors.New("store: conflict")
	ErrRetryable = errors.New("store: retryable")
)

// Page 是统一分页参数。
type Page struct {
	Limit  int
	Cursor string
}

// Session 是一次对话容器。Adapter 字段记录产生它的运行时，用于回放与排障。
type Session struct {
	SessionID string
	ProjectID string
	Adapter   string
	Title     string
	Status    string
	Meta      map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Turn 是一个回合：一次用户输入到 agent 停止。区别于单条 message。
type Turn struct {
	TurnID    string
	SessionID string
	Role      string
	Status    string
	Seq       int
	CreatedAt time.Time
}

// Message 是回合内的单条消息。多模态与工具块放 Parts。
type Message struct {
	MessageID string
	TurnID    string
	Role      string
	Content   string
	Parts     []map[string]any
	CreatedAt time.Time
}

// ToolCall 是一次工具调用。工具统计、审计与回放都从这张表取。
type ToolCall struct {
	CallID     string
	TurnID     string
	ToolName   string
	Input      map[string]any
	Output     map[string]any
	Status     string
	Error      string
	StartedAt  time.Time
	FinishedAt *time.Time
	DurationMS int
}

// Artifact 是会话产物（报告、上传、截图等）。
type Artifact struct {
	ArtifactID string
	SessionID  string
	TurnID     string
	Kind       string
	Path       string
	Size       int64
	SHA256     string
	CreatedAt  time.Time
}

type SessionRepo interface {
	Create(ctx context.Context, s Session) error
	Get(ctx context.Context, sessionID string) (Session, error)
	List(ctx context.Context, projectID string, p Page) ([]Session, string, error)
	UpdateStatus(ctx context.Context, sessionID, status string) error
	Close(ctx context.Context, sessionID string) error
}

type TurnRepo interface {
	Append(ctx context.Context, t Turn) (string, error)
	ListBySession(ctx context.Context, sessionID string, p Page) ([]Turn, string, error)
	GetLatest(ctx context.Context, sessionID string) (Turn, error)
}

type MessageRepo interface {
	Append(ctx context.Context, m Message) (string, error)
	ListByTurn(ctx context.Context, turnID string) ([]Message, error)
	ListBySession(ctx context.Context, sessionID string, p Page) ([]Message, string, error)
}

type ToolCallRepo interface {
	Start(ctx context.Context, c ToolCall) error
	Finish(ctx context.Context, callID, status, errMsg string, output map[string]any, durationMS int) error
	ListByTurn(ctx context.Context, turnID string) ([]ToolCall, error)
	Stats(ctx context.Context, since time.Time) (map[string]int, error)
}

type ArtifactRepo interface {
	Put(ctx context.Context, a Artifact) error
	Get(ctx context.Context, artifactID string) (Artifact, error)
	ListBySession(ctx context.Context, sessionID string) ([]Artifact, error)
}

// Repos 是仓储集合，由 core 注入给 domain 与插件。
type Repos struct {
	Session  SessionRepo
	Turn     TurnRepo
	Message  MessageRepo
	ToolCall ToolCallRepo
	Artifact ArtifactRepo
}
