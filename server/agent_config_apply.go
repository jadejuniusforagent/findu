package server

// 代理运行配置更新的业务编排（批 4 第四刀自 server_mgmt.go pgSaveAgentConfig 下沉）。
//
// handler 只负责 agent 定位、JSON 解码、400/500 映射与响应编码；本文件按原顺序
// 应用部分 PATCH 语义（指针字段缺省=不动），并在 LLM 绑定变更后失效缓存、重建
// live agents，使新配置无需重启即生效。

import (
	"encoding/json"

	"finduai/db"
)

// agentConfigUpdate 是 PATCH /api/agents/{key}/config 的请求体。
//
// 全部字段可选（指针）：例如触发页只提交 trigger_*，未传的 max_turns/run_seconds
// 必须保持原值，不能被重置成 0。
type agentConfigUpdate struct {
	MaxTurns         *int  `json:"max_turns"`
	RunSeconds       *int  `json:"run_seconds"`
	WebSearch        *bool `json:"web_search"`
	InteractiveShell *bool `json:"interactive_shell"`
	// llm_profile_id 三态:字段缺省=不动;显式 null=解绑(跟随任务/全局);数字=绑定该 profile。
	LLMProfileID json.RawMessage `json:"llm_profile_id"`
	// P3 触发后处理策略(三者一起可选,提供任一即整体写入;未提供则不动)。
	TriggerRunMode     *string `json:"trigger_run_mode"`
	TriggerMergeMode   *string `json:"trigger_merge_mode"`
	TriggerMaxParallel *int    `json:"trigger_max_parallel"`
}

// applyAgentConfig applies a partial agent config patch. badRequest=true marks
// client validation failures (400); everything else is a server error (500).
func (s *Server) applyAgentConfig(pg *db.DB, a *db.Agent, req agentConfigUpdate) (badRequest bool, err error) {
	profileChanged := false
	if req.LLMProfileID != nil { // key present (数字 或 null)
		var id *int64
		if err := json.Unmarshal(req.LLMProfileID, &id); err != nil {
			return true, errAgentConfigInvalid("llm_profile_id 格式错误")
		}
		if id != nil { // 绑定:校验目标 profile 有效
			if _, ok := s.loadProfileConfig(*id); !ok {
				return true, errAgentConfigInvalid("指定的 LLM 配置不存在或无效")
			}
		}
		if err := pg.SetAgentLLMProfile(a.Key, id); err != nil {
			return false, err
		}
		profileChanged = true
	}
	if req.MaxTurns != nil {
		mt := *req.MaxTurns
		if mt < 0 {
			mt = 0
		}
		if err := pg.SetAgentMaxTurns(a.Key, mt); err != nil {
			return false, err
		}
	}
	if req.RunSeconds != nil {
		rs := *req.RunSeconds
		if rs < 0 {
			rs = 0
		}
		if err := pg.SetAgentRunSeconds(a.Key, rs); err != nil {
			return false, err
		}
	}
	if req.WebSearch != nil {
		if err := pg.SetAgentWebSearch(a.Key, *req.WebSearch); err != nil {
			return false, err
		}
	}
	if req.InteractiveShell != nil {
		if err := pg.SetAgentInteractiveShell(a.Key, *req.InteractiveShell); err != nil {
			return false, err
		}
	}
	// P3 触发策略:三者作为一组写入(SetAgentTriggerBehavior 一次写三列),缺的字段用
	// 当前存量值回填,避免只传一个把另两个覆盖成默认。
	if req.TriggerRunMode != nil || req.TriggerMergeMode != nil || req.TriggerMaxParallel != nil {
		runMode, mergeMode, maxPar := a.TriggerRunMode, a.TriggerMergeMode, a.TriggerMaxParallel
		if req.TriggerRunMode != nil {
			runMode = *req.TriggerRunMode
		}
		if req.TriggerMergeMode != nil {
			mergeMode = *req.TriggerMergeMode
		}
		if req.TriggerMaxParallel != nil {
			maxPar = *req.TriggerMaxParallel
		}
		if err := pg.SetAgentTriggerBehavior(a.Key, runMode, mergeMode, maxPar); err != nil {
			return false, err
		}
	}
	// an agent's LLM binding change also invalidates pinned-task / per-profile caches
	// so their next round re-resolves each agent's bound model.
	if profileChanged {
		s.invalidateProfileAgents()
	}
	// Task bundles capture operational Agent settings (turn/time budgets, tools,
	// web search). Rebuild them even when no global profile is configured.
	s.invalidateTaskAgents()
	// rebuild the live agents so the new max_turns/run_seconds/binding apply without a restart.
	s.cfgMu.Lock()
	cfg, on := s.llmCfg, s.llmOn
	s.cfgMu.Unlock()
	if on {
		_ = s.applyLLM(cfg)
	}
	return false, nil
}

// errAgentConfigInvalid 是输入校验错误的薄类型：只为与「服务端错误」区分，
// 错误文案原样返回给客户端。
type errAgentConfigInvalid string

func (e errAgentConfigInvalid) Error() string { return string(e) }
