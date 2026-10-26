package server

// 设置接口的业务编排（批 4 第三刀自 server.go putSettings 下沉）。
//
// handler 只负责 JSON 解码、400/500 映射与响应编码；本文件按原顺序逐段应用
// 各设置项，返回值区分「客户端校验失败（400）」与「服务端失败（500）」，
// 行为、错误文案、即时生效/重建 agent 的时序均与下沉前一致。

import (
	"strconv"
	"strings"
)

// settingsUpdate 是 PUT /api/settings 的请求体。全部字段用指针以区分
// 「没传」与「传了零值」——部分 PUT 只改给定项。
type settingsUpdate struct {
	TrafficCapture      *bool `json:"traffic_capture"`
	AgentTrafficBinding *bool `json:"agent_traffic_binding"`
	LLMRecord           *bool `json:"llm_record"` // LLM 录制开关（默认关）；即时生效，无需重建 agent
	// Web search. WebSearchEnabled/Backend toggle the tool + backend; BraveKey/TavilyKey
	// are optional — omit (null) to leave a stored key untouched, send "" to clear.
	WebSearchEnabled *bool   `json:"web_search_enabled"`
	WebSearchBackend *string `json:"web_search_backend"`
	BraveKey         *string `json:"brave_search_api_key"`
	TavilyKey        *string `json:"tavily_search_api_key"`
	WebSearchProxy   *string `json:"web_search_proxy"`   // 独立出口代理(http/https/socks5)；null=不改，""=清空
	GlobalProxy      *string `json:"global_proxy"`       // 全局出口代理(http/https/socks5)；null=不改，""=清空(直连)
	PythonInterp     *string `json:"python_interpreter"` // 自定义脚本工具的 python 解释器路径
	Workers          *int    `json:"workers"`            // 并发工作 agent 数(>0)；对之后启动的任务生效
	// 任务并发上限:同时「运行中」的任务数上限。关闭=不限;开启后新建任务超限则排队,有空位自动启动。
	ConcurrencyEnabled *bool `json:"task_concurrency_enabled"`
	ConcurrencyLimit   *int  `json:"task_concurrency_limit"`
	// LLM 轮询(故障转移)开关 + 「绑定配置失败也兜底回轮询链」开关。两者都需要
	// 重建 provider 链才生效，走下面的 changed → applyLLM 路径。
	LLMPoolEnabled      *bool `json:"llm_pool_enabled"`
	LLMPoolBindFallback *bool `json:"llm_pool_bind_fallback"`
	// 操作约束注入范围开关(默认都开);即时生效(planner/worker 每轮读),无需重建 agent。
	ConstraintsInjectPlanner *bool `json:"constraints_inject_planner"`
	ConstraintsInjectWorker  *bool `json:"constraints_inject_worker"`
	// 实验功能:noa 上下文压缩开关(默认关);每 run 读,对之后启动的 run 生效,无需重建 agent。
	NoaCompaction *bool `json:"noa_compaction"`
	// 漏洞 IM 推送的全局项。三者都由投递引擎每轮读一次，改完即时生效，
	// 不需要重建 agent 或重启。
	NotifyEnabled    *bool   `json:"notify_enabled"`
	NotifyBaseURL    *string `json:"notify_public_base_url"`
	NotifyDigestMins *int    `json:"notify_digest_interval_min"`
}

// applySettings 按请求中的字段逐项应用设置。返回 badRequest=true 表示错误应
// 以 400 回给客户端（输入校验失败），否则为服务端错误（500）。
func (s *Server) applySettings(req settingsUpdate) (badRequest bool, err error) {
	if req.ConstraintsInjectPlanner != nil {
		if err := s.m.pg.SetBool(settingConstraintsInjectPlanner, *req.ConstraintsInjectPlanner); err != nil {
			return false, err
		}
	}
	if req.ConstraintsInjectWorker != nil {
		if err := s.m.pg.SetBool(settingConstraintsInjectWorker, *req.ConstraintsInjectWorker); err != nil {
			return false, err
		}
	}
	if req.NoaCompaction != nil {
		// 每 run 读的解析器,切换即时对之后启动的 run 生效,无需 applyLLM 重建。
		if err := s.m.SetNoaCompaction(*req.NoaCompaction); err != nil {
			return false, err
		}
	}
	// 推送全局项:投递引擎每轮重新读取,所以即时生效、无需重启。
	if req.NotifyEnabled != nil {
		if err := s.m.pg.SetBool(settingNotifyEnabled, *req.NotifyEnabled); err != nil {
			return false, err
		}
	}
	if req.NotifyBaseURL != nil {
		// 统一裁掉尾部斜杠:回链拼接用的是 fmt.Sprintf("%s/function/..."),
		// 留着尾部斜杠会产出 "//function/..." 这种双斜杠路径。
		base := trimTrailingSlash(strings.TrimSpace(*req.NotifyBaseURL))
		if base != "" && !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			return true, errSettingInvalid("回链地址需以 http:// 或 https:// 开头")
		}
		if err := s.m.pg.SetSetting(settingNotifyPublicBaseURL, base); err != nil {
			return false, err
		}
	}
	if req.NotifyDigestMins != nil {
		// 下限 1 分钟:更短的周期等于实时推送,那样应该直接把渠道改成 realtime 模式。
		if *req.NotifyDigestMins < 1 || *req.NotifyDigestMins > 24*60 {
			return true, errSettingInvalid("汇总周期需在 1 到 1440 分钟之间")
		}
		if err := s.m.pg.SetSetting(settingNotifyDigestMinutes, strconv.Itoa(*req.NotifyDigestMins)); err != nil {
			return false, err
		}
	}
	if req.Workers != nil {
		if err := s.m.SetWorkers(*req.Workers); err != nil {
			return true, err
		}
	}
	if req.ConcurrencyEnabled != nil || req.ConcurrencyLimit != nil {
		// 部分 PUT:未给的字段用当前值兜底,避免只改一个把另一个重置。
		curOn, curLimit := s.m.ConcurrencyLimit()
		if curLimit == 0 {
			curLimit = defaultConcurrencyLimit
		}
		on, limit := curOn, curLimit
		if req.ConcurrencyEnabled != nil {
			on = *req.ConcurrencyEnabled
		}
		if req.ConcurrencyLimit != nil {
			limit = *req.ConcurrencyLimit
		}
		if err := s.m.SetConcurrency(on, limit); err != nil {
			return true, err
		}
		// 立即协调一次:关闭时放行全部排队,调高上限时补位启动,不必等下一个 tick。
		go s.reconcileConcurrency()
	}
	if req.PythonInterp != nil {
		if err := s.m.pg.SetSetting(settingPythonInterp, strings.TrimSpace(*req.PythonInterp)); err != nil {
			return false, err
		}
	}
	if req.LLMRecord != nil {
		// 录制器每次调用读取该标志，切换即时生效，无需 applyLLM 重建。
		if err := s.m.SetLLMRecordEnabled(*req.LLMRecord); err != nil {
			return false, err
		}
	}
	changed := false
	if req.LLMPoolEnabled != nil {
		if err := s.m.SetLLMPoolEnabled(*req.LLMPoolEnabled); err != nil {
			return false, err
		}
		changed = true // the provider chain itself changes shape → rebuild
	}
	if req.LLMPoolBindFallback != nil {
		if err := s.m.SetLLMPoolBindFallback(*req.LLMPoolBindFallback); err != nil {
			return false, err
		}
		changed = true
	}
	if req.LLMPoolEnabled != nil || req.LLMPoolBindFallback != nil {
		// Pinned tasks' planner/worker and per-profile chat agents hold providers
		// built under the OLD switch state — drop them so they pick up the new one.
		s.invalidateProfileAgents()
	}
	if req.AgentTrafficBinding != nil {
		if err := s.m.pg.SetBool(settingAgentTrafficBinding, *req.AgentTrafficBinding); err != nil {
			return false, err
		}
	}
	if req.TrafficCapture != nil {
		if err := s.m.SetTrafficEnabled(*req.TrafficCapture); err != nil {
			return false, err
		}
		changed = true
	}
	if req.GlobalProxy != nil {
		// Validation failure (bad scheme/host) is a client error, not a 500.
		if err := s.m.SetGlobalProxy(*req.GlobalProxy); err != nil {
			return true, err
		}
		changed = true // capture-off egress is baked into agents at build time → rebuild
	}
	if req.WebSearchEnabled != nil || req.WebSearchBackend != nil || req.BraveKey != nil || req.TavilyKey != nil || req.WebSearchProxy != nil {
		// Fill unspecified fields from current state so a partial PUT doesn't reset them.
		on, backend, _, _, _ := s.m.WebSearch()
		if req.WebSearchEnabled != nil {
			on = *req.WebSearchEnabled
		}
		if req.WebSearchBackend != nil {
			backend = *req.WebSearchBackend
		}
		if err := s.m.SetWebSearch(on, backend, req.BraveKey, req.TavilyKey, req.WebSearchProxy); err != nil {
			return false, err
		}
		changed = true
	}
	if changed {
		// rebuild agents so the new proxy/tools/prompt/web-search take hold (only if LLM configured).
		s.cfgMu.Lock()
		cfg, on := s.llmCfg, s.llmOn
		s.cfgMu.Unlock()
		if on {
			if err := s.applyLLM(cfg); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

// errSettingInvalid 是输入校验错误的薄类型：只为与「服务端错误」区分，
// 错误文案原样返回给客户端。
type errSettingInvalid string

func (e errSettingInvalid) Error() string { return string(e) }
