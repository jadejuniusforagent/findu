package db

import (
	"finduai/domain/asset"
	"finduai/domain/task"
)

// 资产拦截规则的匹配/执行层 —— 装配层。规则存储与纯匹配引擎在
// domain/asset/intercept.go（批 3 第八刀）；这里保留跨域装配：
// CheckAssetsIntercept 要把 全局规则表 ∪ 任务级规则表 拼成闸门输入，
// 三张表分属资产与任务两个域，故留 db。db 侧包级转发让 agent/server
// 调用点不动（别名统一收口在批 4/5）。

// ListAssetInterceptRules 是 *DB 同名方法的透传，让只持有 AssetStore 的调用方
// （如 agent 工具）也能读取规则。
func (s *AssetStore) ListAssetInterceptRules() ([]task.InterceptRule, error) {
	return s.db.ListAssetInterceptRules()
}

// CheckAssetsIntercept 按 id 载入资产，逐个执行「先拦截后允许」闸门判定，返回所有
// 被拒的资产。拦截规则 = 全局 ∪ 任务级 block；允许规则 = 任务级 allow（仅本任务）。
// 无 id 时快速返回。用全局 GetByIDs（不受任务范围过滤）以保证拦截不被 scope 削弱。
func (s *AssetStore) CheckAssetsIntercept(taskID int64, ids []string) ([]asset.AssetInterceptHit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	blockRules, err := s.db.ListAssetInterceptRules()
	if err != nil {
		return nil, err
	}
	var allowRules []task.InterceptRule
	if taskID > 0 {
		tb, ta, err := s.TaskInterceptRulesSplit(taskID)
		if err != nil {
			return nil, err
		}
		blockRules = append(blockRules, tb...)
		allowRules = ta
	}
	// 既无拦截规则、也无启用的允许规则 → 无需判定，全部放行。
	if len(blockRules) == 0 && !asset.HasEnabledRule(allowRules) {
		return nil, nil
	}
	assets, err := s.GetByIDs(ids)
	if err != nil {
		return nil, err
	}
	var hits []asset.AssetInterceptHit
	for _, a := range assets {
		domains, ips, urls := a.InterceptCandidates()
		if d := asset.EvaluateAssetGate(blockRules, allowRules, domains, ips, urls); !d.Allowed {
			hits = append(hits, asset.AssetInterceptHit{Asset: a, Reason: d.Reason})
		}
	}
	return hits, nil
}
