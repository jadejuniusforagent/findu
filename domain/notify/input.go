package notify

// 渠道创建/更新的业务语义（批 4 第二刀自 server/notify_api.go 下沉）。
//
// 请求体字段全部用指针，以区分「没传」与「传了零值」：PATCH 语义下，没传的
// 字段必须保留库中原值；显式传 0 是合法配置（限流 0 = 不限流）。
// handler 只负责 JSON 解码与响应编码，错误文案保持原样（直接 400 回给用户）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	appnotify "finduai/notify"
)

// ChannelInput is the HTTP-facing create/update payload for a notification
// channel. Field names match the API JSON exactly so handlers decode straight
// into it.
type ChannelInput struct {
	Name       *string           `json:"name"`
	Kind       *string           `json:"kind"`
	Enabled    *bool             `json:"enabled"`
	Mode       *string           `json:"mode"`
	Config     map[string]any    `json:"config"`
	Filter     *appnotify.Filter `json:"filter"`
	RatePerMin *int              `json:"rate_per_min"`
}

// NewChannelFromInput builds a channel from the create payload: channel-type
// validation, default mode/rate, and filter/config JSON encoding. Returned errors
// are user-facing (400), so the handler writes err.Error() verbatim.
func NewChannelFromInput(in ChannelInput) (*NotificationChannel, error) {
	if in.Kind == nil || !appnotify.ValidKind(*in.Kind) {
		return nil, fmt.Errorf("渠道类型无效，可选：%s", strings.Join(appnotify.Kinds(), " / "))
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		return nil, errors.New("缺少渠道名称")
	}
	channel, _ := appnotify.Get(*in.Kind)
	if err := channel.Validate(in.Config); err != nil {
		return nil, err
	}
	ch := &NotificationChannel{
		Name:       name,
		Kind:       *in.Kind,
		Enabled:    in.Enabled,
		Mode:       NotifyModeRealtime,
		RatePerMin: channel.DefaultRatePerMin(),
	}
	if in.Mode != nil {
		if !ValidNotifyMode(*in.Mode) {
			return nil, errors.New("推送模式无效，可选：realtime / digest")
		}
		ch.Mode = *in.Mode
	}
	if in.RatePerMin != nil {
		// 显式给值就照用——包括 0，它表示「不限流」，是合法配置。
		if *in.RatePerMin < 0 {
			return nil, errors.New("限流值不能为负")
		}
		ch.RatePerMin = *in.RatePerMin
	}
	if in.Filter != nil {
		// 写入时校验取值受限的过滤字段（如 min_severity）。门槛打错字会让过滤器
		// 静默失效变成全推，必须在入口拦掉。
		if err := in.Filter.Validate(); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(in.Filter)
		ch.Filter = raw
	}
	rawCfg, _ := json.Marshal(in.Config)
	ch.Config = rawCfg
	return ch, nil
}

// ApplyChannelInput merges an update payload into the stored channel. kind may
// change, but that replaces the whole credential set and must not be merged with
// the old config. PrepareConfigUpdate (not MergeConfig) is used so that a target
// address change forces the operator to re-state credentials — otherwise
// "change address only, reuse credentials" would send real secrets to a new host.
//
// enabledChanged reports that the enabled flag actually flipped; the caller must
// persist the config first and then toggle the switch (which also skips pending
// deliveries), preserving the no-window ordering documented at the call site.
func ApplyChannelInput(current *NotificationChannel, in ChannelInput) (updated *NotificationChannel, enabledChanged bool, err error) {
	kind := current.Kind
	if in.Kind != nil {
		if !appnotify.ValidKind(*in.Kind) {
			return nil, false, fmt.Errorf("渠道类型无效，可选：%s", strings.Join(appnotify.Kinds(), " / "))
		}
		kind = *in.Kind
	}
	channel, _ := appnotify.Get(kind)

	var stored map[string]any
	if kind == current.Kind {
		if len(current.Config) > 0 {
			_ = json.Unmarshal(current.Config, &stored)
		}
	}
	if stored == nil {
		stored = map[string]any{}
	}
	merged, err := appnotify.PrepareConfigUpdate(kind, stored, in.Config)
	if err != nil {
		return nil, false, err
	}
	if err := channel.Validate(merged); err != nil {
		return nil, false, err
	}
	rawCfg, _ := json.Marshal(merged)

	ch := &NotificationChannel{
		ID:         current.ID,
		Name:       current.Name,
		Kind:       kind,
		Enabled:    current.Enabled,
		Mode:       current.Mode,
		Config:     rawCfg,
		Filter:     current.Filter,
		RatePerMin: current.RatePerMin,
	}
	if in.Name != nil {
		if ch.Name = strings.TrimSpace(*in.Name); ch.Name == "" {
			return nil, false, errors.New("渠道名称不能为空")
		}
	}
	if in.Enabled != nil {
		ch.Enabled = in.Enabled
	}
	if in.Mode != nil {
		if !ValidNotifyMode(*in.Mode) {
			return nil, false, errors.New("推送模式无效，可选：realtime / digest")
		}
		ch.Mode = *in.Mode
	}
	if in.RatePerMin != nil {
		if *in.RatePerMin < 0 {
			return nil, false, errors.New("限流值不能为负")
		}
		ch.RatePerMin = *in.RatePerMin
	}
	if in.Filter != nil {
		if err := in.Filter.Validate(); err != nil {
			return nil, false, err
		}
		raw, _ := json.Marshal(in.Filter)
		ch.Filter = raw
	}
	enabledChanged = ch.Enabled != nil && current.Enabled != nil && *ch.Enabled != *current.Enabled
	return ch, enabledChanged, nil
}
