package server

import (
	"encoding/json"
	"errors"
	dnotify "finduai/domain/notify"
	"fmt"
	"net/http"
	"time"

	"finduai/db"
	"finduai/notify"
)

// 本文件是推送功能的 HTTP 接口。全部路由挂在 requireAuth 之后（见 Handler()），
// 与其它管理接口一致。

// notifyChannelDTO 是渠道的对外表述。
//
// Config 是**掩码后**的配置：凭据字段被替换成 notify.MaskedPrefix 开头的值。
// 前端把掩码值原样提交回来即表示「这个字段没改」，服务端据此保留库中原值
// （见 notify.MergeConfig）。
type notifyChannelDTO struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Enabled    bool           `json:"enabled"`
	Mode       string         `json:"mode"`
	Config     map[string]any `json:"config"`
	Filter     notify.Filter  `json:"filter"`
	RatePerMin int            `json:"rate_per_min"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	// SecretKeys 告知前端哪些字段是凭据，据此渲染密码框与「留空即不改」的提示。
	// 由渠道自己声明（notify.Channel.SecretKeys），前端不硬编码渠道知识。
	SecretKeys []string `json:"secret_keys"`
}

// notifyDeliveryDTO 是投递历史的对外表述。
type notifyDeliveryDTO struct {
	ID          int64      `json:"id"`
	FindingID   int64      `json:"finding_id,string"`
	EventKind   string     `json:"event_kind"`
	ChannelID   int64      `json:"channel_id"`
	ChannelName string     `json:"channel_name"`
	ChannelKind string     `json:"channel_kind"`
	State       string     `json:"state"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error"`
	BatchID     *int64     `json:"batch_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
	NextAttempt time.Time  `json:"next_attempt_at"`
	// 消息标题摘要，让历史列表不必展开就能看出这条推的是什么。
	Title    string `json:"title"`
	Severity string `json:"severity"`
}

func toNotifyChannelDTO(ch *dnotify.NotificationChannel) notifyChannelDTO {
	var cfg map[string]any
	if len(ch.Config) > 0 {
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	secrets := []string{}
	if c, ok := notify.Get(ch.Kind); ok {
		secrets = c.SecretKeys()
	}
	return notifyChannelDTO{
		ID:         ch.ID,
		Name:       ch.Name,
		Kind:       ch.Kind,
		Enabled:    ch.IsEnabled(),
		Mode:       ch.Mode,
		Config:     notify.MaskConfig(ch.Kind, cfg),
		Filter:     notify.ParseFilter(ch.Filter),
		RatePerMin: ch.RatePerMin,
		CreatedAt:  ch.CreatedAt,
		UpdatedAt:  ch.UpdatedAt,
		SecretKeys: secrets,
	}
}

func toNotifyDeliveryDTO(dl *dnotify.NotificationDelivery) notifyDeliveryDTO {
	snap, _ := parseSnapshot(dl)
	dto := notifyDeliveryDTO{
		ID:          dl.ID,
		FindingID:   dl.FindingID,
		EventKind:   dl.EventKind,
		ChannelID:   dl.ChannelID,
		ChannelName: dl.ChannelName,
		ChannelKind: dl.ChannelKind,
		State:       dl.State,
		Attempts:    dl.Attempts,
		LastError:   dl.LastError,
		BatchID:     dl.BatchID,
		CreatedAt:   dl.CreatedAt,
		SentAt:      dl.SentAt,
		NextAttempt: dl.NextAttemptAt,
		Severity:    snap.Severity,
	}
	if snap.Name != "" {
		dto.Title = snap.Name
	} else {
		dto.Title = snap.VulnClass
	}
	return dto
}

// notifyMeta 返回通知页需要的静态元数据与全局设置，一次请求拿全，
// 避免前端为了渲染一个下拉框发三次请求。
func (s *Server) notifyMeta(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	kinds := make([]map[string]any, 0, len(notify.Kinds()))
	for _, k := range notify.Kinds() {
		ch, _ := notify.Get(k)
		kinds = append(kinds, map[string]any{
			"kind":                 k,
			"default_rate_per_min": ch.DefaultRatePerMin(),
			"secret_keys":          ch.SecretKeys(),
		})
	}
	baseURL, _, _ := pg.GetSetting(settingNotifyPublicBaseURL)
	digest, _, _ := pg.GetSetting(settingNotifyDigestMinutes)
	stats, err := pg.NotificationStatsSnapshot(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"kinds":               kinds,
		"enabled":             pg.GetBool(settingNotifyEnabled, true),
		"public_base_url":     baseURL,
		"digest_interval_min": digest,
		"defaults": map[string]any{
			"digest_interval_min": notifyDefaultDigestMinutes,
		},
		"stats": stats,
	})
}

func (s *Server) notifyListChannels(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	channels, err := pg.ListNotificationChannels(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]notifyChannelDTO, 0, len(channels))
	for _, ch := range channels {
		out = append(out, toNotifyChannelDTO(ch))
	}
	writeJSON(w, 200, map[string]any{"channels": out})
}

// notifyChannelRequest 已下沉 domain/notify.ChannelInput（批 4 第二刀）——
// 请求体直接解码为 dnotify.ChannelInput，业务在 NewChannelFromInput /
// ApplyChannelInput，handler 只留解码与响应编码。

func (s *Server) notifyCreateChannel(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var in dnotify.ChannelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "请求体不是合法 JSON: "+err.Error())
		return
	}
	ch, err := dnotify.NewChannelFromInput(in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id, err := pg.SaveNotificationChannel(r.Context(), ch)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) notifyUpdateChannel(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "渠道 id 无效")
		return
	}
	current, err := pg.NotificationChannelByID(r.Context(), id)
	if err != nil {
		notifyChannelLookupErr(w, err)
		return
	}
	var in dnotify.ChannelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "请求体不是合法 JSON: "+err.Error())
		return
	}
	ch, enabledChanged, err := dnotify.ApplyChannelInput(current, in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// 走 SetNotificationChannelEnabled 而非 SaveNotificationChannel 的路径，
	// 是为了让「停用」同时把存量待发投递标记为 skipped，避免重新启用时收到
	// 一批已过时的积压消息。
	if enabledChanged {
		// 先把配置更新落库（此时 enabled 用旧值，避免提前触发跳过逻辑），
		// 再单独切开关。两步之间没有并发窗口：本接口是唯一改这两个字段的入口。
		prev := ch.Enabled
		ch.Enabled = current.Enabled
		if _, err := pg.SaveNotificationChannel(r.Context(), ch); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if err := pg.SetNotificationChannelEnabled(r.Context(), id, *prev); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"id": id})
		return
	}
	if _, err := pg.SaveNotificationChannel(r.Context(), ch); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) notifyDeleteChannel(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "渠道 id 无效")
		return
	}
	if err := pg.DeleteNotificationChannel(r.Context(), id); err != nil {
		notifyChannelLookupErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// notifyTestChannel 用当前保存的配置发一条测试消息。
//
// 直接调用渠道 Send 而不经投递队列：测试的目的是立刻告诉用户「这套配置能不能
// 发出去」，走队列会把结果藏进投递历史，用户得再去翻一遍才知道成没成。
// 因此本接口是**同步**的，超时上限由 notify 包的 HTTP 客户端决定（15 秒）。
func (s *Server) notifyTestChannel(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "渠道 id 无效")
		return
	}
	ch, err := pg.NotificationChannelByID(r.Context(), id)
	if err != nil {
		notifyChannelLookupErr(w, err)
		return
	}
	channel, ok := notify.Get(ch.Kind)
	if !ok {
		writeErr(w, 400, fmt.Sprintf("渠道类型 %q 未注册", ch.Kind))
		return
	}
	var cfg map[string]any
	if len(ch.Config) > 0 {
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if err := channel.Validate(cfg); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	msg := notifyTestMessage(s.notifierBaseURL(pg))
	start := time.Now()
	// 测试消息只有一条，送达条数这里不需要（渠道长度上限对单条消息而言
	// 由截断兜底，不涉及分段）。
	if _, err := channel.Send(r.Context(), cfg, msg); err != nil {
		// 把渠道返回的原始错误如实回给用户——这是他们调试配置的唯一线索。
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":         true,
		"latency_ms": time.Since(start).Milliseconds(),
	})
}

// notifyTestMessage 构造测试消息。刻意用一眼能看出是测试的内容：
// 收到的人不应该把它误判成真实漏洞。
func notifyTestMessage(baseURL string) notify.Message {
	return notify.Message{
		Items: []notify.Item{{
			FindingID: 0,
			Name:      "测试消息 · 渠道配置正常",
			VulnClass: "连通性测试",
			Severity:  "low",
			Summary:   "这是 FINDU 推送渠道的测试消息，收到即表示该渠道配置可用。",
			Assets:    []string{"findu.example.com"},
			DetailURL: baseURL,
		}},
		HomeURL: baseURL,
	}
}

// notifierBaseURL 读回链用的外部地址。
func (s *Server) notifierBaseURL(pg *db.DB) string {
	v, _, _ := pg.GetSetting(settingNotifyPublicBaseURL)
	return trimTrailingSlash(v)
}

func (s *Server) notifyListDeliveries(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	f := dnotify.NotificationDeliveryFilter{
		State:     r.URL.Query().Get("state"),
		EventKind: r.URL.Query().Get("event_kind"),
	}
	if v := r.URL.Query().Get("channel_id"); v != "" {
		f.ChannelID = int64(atoiDefault(v, 0))
	}
	page := queryInt(r, "page", 1)
	pageSize := queryInt(r, "page_size", 50)
	items, total, err := pg.ListNotificationDeliveries(r.Context(), f, page, pageSize)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]notifyDeliveryDTO, 0, len(items))
	for _, dl := range items {
		out = append(out, toNotifyDeliveryDTO(dl))
	}
	writeJSON(w, 200, map[string]any{"deliveries": out, "total": total, "page": page, "page_size": pageSize})
}

func (s *Server) notifyRetryDelivery(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "投递 id 无效")
		return
	}
	if err := pg.RetryNotificationDelivery(r.Context(), id); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// notifyChannelLookupErr 把「渠道不存在」翻译成 404，其余错误 500。
func notifyChannelLookupErr(w http.ResponseWriter, err error) {
	if errors.Is(err, dnotify.ErrNotificationChannelNotFound) {
		writeErr(w, 404, "通知渠道不存在")
		return
	}
	writeErr(w, 500, err.Error())
}
