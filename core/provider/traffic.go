package provider

import (
	"context"
	"time"
)

// CaptureFilter 描述抓包过滤条件。零值表示全量。
type CaptureFilter struct {
	Method      string `json:"method,omitempty"`
	Host        string `json:"host,omitempty"`
	URLContains string `json:"url_contains,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// Capture 是一次抓包会话。
type Capture struct {
	ID        string        `json:"id"`
	Filter    CaptureFilter `json:"filter"`
	Status    string        `json:"status"` // running / stopped
	StartedAt time.Time     `json:"started_at"`
}

// Flow 是一条已录制的请求/响应。Body 用 []byte，JSON 侧由实现自行编码；
// 宿主只做转发与展示，不解释协议。
type Flow struct {
	ID              string              `json:"id"`
	CaptureID       string              `json:"capture_id,omitempty"`
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	Host            string              `json:"host"`
	StatusCode      int                 `json:"status_code"`
	RequestHeaders  map[string][]string `json:"request_headers,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	RequestBody     []byte              `json:"request_body,omitempty"`
	ResponseBody    []byte              `json:"response_body,omitempty"`
	CapturedAt      time.Time           `json:"captured_at"`
}

// FlowCriteria 用于查询历史流量。零值表示不过滤、不限量。
type FlowCriteria struct {
	Method      string `json:"method,omitempty"`
	Host        string `json:"host,omitempty"`
	URLContains string `json:"url_contains,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// Mutation 是对重放请求的改写。空字段表示沿用原值。
type Mutation struct {
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// TrafficProvider 是流量能力接口（contracts/capability-providers.md §四）。
// 实现自持 traffic_evidence_snapshots 表。
type TrafficProvider interface {
	StartCapture(ctx context.Context, filter CaptureFilter) (Capture, error)
	StopCapture(ctx context.Context, id string) error

	Query(ctx context.Context, criteria FlowCriteria) ([]Flow, error)
	Get(ctx context.Context, id string) (Flow, error)

	Replay(ctx context.Context, id string, mutation Mutation) (Flow, error)
}
