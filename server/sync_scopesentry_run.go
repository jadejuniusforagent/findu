package server

// ScopeSentry 同步编排（批 4 第六刀自 sync_scopesentry.go syncSSRun 下沉）。
//
// handler 只负责依赖可用性检查（pg/assetStore/companyStore）、JSON 解码、超时
// 上下文与响应；本文件按原顺序执行项目/任务维度筛选、资产分页拉取、公司创建与
// 归属重建。返回 (响应体, HTTP 状态, 错误文案)：errMsg=="" 表示成功。

import (
	"context"
	"fmt"

	"finduai/db"
)

func (s *Server) runScopeSentrySync(ctx context.Context, as *db.AssetStore, cs *db.CompanyStore, req ssSyncReq) (map[string]any, int, string) {
	if req.Dimension != "project" && req.Dimension != "task" {
		return nil, 400, "dimension 必须是 project 或 task"
	}
	if len(req.Targets) == 0 {
		return nil, 400, "targets 不能为空"
	}
	if len(req.AssetTypes) == 0 {
		req.AssetTypes = []string{"subdomain", "service", "app"}
	}
	pageSize := req.PageSize
	if pageSize <= 0 || pageSize > 500 {
		pageSize = syncDefaultPage
	}

	cl, err := s.scopeSentryClient(ctx)
	if err != nil {
		return nil, 502, err.Error()
	}
	defer cl.Close()

	synced := map[string]int{"subdomain": 0, "app": 0, "service": 0, "ip": 0}
	var companies []string
	var warnings, errs []string
	madeCompany := false

	for _, target := range req.Targets {
		filter := map[string]any{}
		switch req.Dimension {
		case "project":
			filter["project"] = []string{target}
			if req.CreateCompany {
				name, roots, perr := s.ssProjectMeta(ctx, cl, target)
				if perr != nil {
					warnings = append(warnings, fmt.Sprintf("项目 %s 详情获取失败: %v", target, perr))
				} else if name != "" {
					cid, cerr := cs.UpsertByName(name)
					if cerr != nil {
						warnings = append(warnings, fmt.Sprintf("公司 %s 创建失败: %v", name, cerr))
					} else {
						companies = append(companies, name)
						madeCompany = true
						if len(roots) > 0 {
							cs.AddScope(cid, roots, "scopesentry:"+name)
						}
					}
				}
			}
		case "task":
			filter["task"] = []string{target}
		}

		for _, at := range req.AssetTypes {
			ssType, ok := map[string]string{"subdomain": "subdomain", "service": "asset", "app": "app"}[at]
			if !ok {
				warnings = append(warnings, "未知资产类型，已跳过: "+at)
				continue
			}
			items, truncated, ferr := s.ssPageAll(ctx, cl, ssType, filter, pageSize)
			if ferr != nil {
				errs = append(errs, fmt.Sprintf("%s(%s) 拉取失败: %v", at, target, ferr))
				continue
			}
			if truncated {
				warnings = append(warnings, fmt.Sprintf("%s(%s) 达到 %d 条上限，已截断", at, target, syncMaxPerType))
			}
			for _, raw := range items {
				if e := s.ssIngest(as, at, raw, synced); e != "" {
					errs = append(errs, e)
				}
			}
		}
	}

	// Rebuild company attribution so freshly-synced assets attach to their company
	// (scope was written above; assets came after).
	if madeCompany {
		_ = cs.RecomputeAttribution()
	}

	return map[string]any{
		"synced":    synced,
		"companies": companies,
		"warnings":  warnings,
		"errors":    errs,
	}, 0, ""
}
