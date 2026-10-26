package db

import (
	"database/sql"
	"net/url"
	"sort"
	"strings"

	"finduai/core/store"
	"finduai/domain/asset"
	"finduai/domain/company"
)

// =====================================================================
// 统一资产表 —— 装配层
//
// 值类型 / upsert 家族 / 查询删除统计 / DSL 查询引擎 / 拦截规则与匹配
// 已迁入 domain/asset（批 3 第八刀，见 docs/refactor/STEP3-MIGRATION-MAP.md §十八）。
// 本文件保留：
//   - 值类型别名（外部调用点不动，别名统一收口在批 4/5）；
//   - AssetStore 结构与事务协议：withCompanyScopeMutation 串起 company_scope
//     归属锁与资产写入（归属解析属资产×企业交叉域，company_resolver 适配注入，
//     等 companies 域迁出后一起下沉）；
//   - 需要任务域注水/CTE 的方法（QueryDSL 的注水在本文件，QueryDSLInScope/
//     GetByIDsInScope 在 asset_dsl.go）；
//   - 任务删除路径的跨域 host 统计（读 tasks/exploration_anchors）。
// =====================================================================

// AssetStore operates on the assets table.
type AssetStore struct {
	db      *DB
	company *CompanyStore
	tx      *sql.Tx
}

// Assets returns the asset store.
func (d *DB) Assets() *AssetStore {
	return &AssetStore{db: d, company: d.Companies()}
}

// Companies returns the company store associated with this asset store.
func (s *AssetStore) Companies() *CompanyStore { return s.company }

// DB 暴露底层句柄给跨域只读工具(如漏洞库检索);写操作仍走各 Store。
func (s *AssetStore) DB() *DB { return s.db }

// assetCompanyResolver 把 db 侧的 company_scope 归属解析注入资产域 upsert
// 家族（companies 域迁出后由 domain/company 承载，批 3 第十刀）。
type assetCompanyResolver struct{}

func (assetCompanyResolver) ResolveCompany(q store.Querier, rootDomain, ipStr, icp string) (*int64, error) {
	return company.ResolveCompanyWithICP(q, rootDomain, ipStr, icp)
}

// withCompanyScopeMutation serializes scope resolution and every asset write
// that consumes its result in one transaction. Nested asset side effects reuse
// the same transaction through the scoped store.
func (s *AssetStore) withCompanyScopeMutation(fn func(*AssetStore) (string, error)) (string, error) {
	if s.tx != nil {
		return fn(s)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := company.LockScopeMutation(tx); err != nil {
		return "", err
	}
	scoped := &AssetStore{db: s.db, company: s.company, tx: tx}
	id, err := fn(scoped)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// =====================================================================
// upsert 家族转发（事务装配在本层，域侧只收 q + resolver）
// =====================================================================

// UpsertRootDomain idempotently inserts or merges a root domain asset.
func (s *AssetStore) UpsertRootDomain(req asset.UpsertRootDomainReq) (string, error) {
	if s.tx != nil {
		return asset.UpsertRootDomain(s.tx, assetCompanyResolver{}, req)
	}
	return s.withCompanyScopeMutation(func(scoped *AssetStore) (string, error) {
		return asset.UpsertRootDomain(scoped.tx, assetCompanyResolver{}, req)
	})
}

// UpsertIP idempotently inserts or merges an IP asset.
func (s *AssetStore) UpsertIP(req asset.UpsertIPReq) (string, error) {
	if s.tx != nil {
		return asset.UpsertIP(s.tx, assetCompanyResolver{}, req)
	}
	return s.withCompanyScopeMutation(func(scoped *AssetStore) (string, error) {
		return asset.UpsertIP(scoped.tx, assetCompanyResolver{}, req)
	})
}

// AppendIPPort appends a {port, service} entry to an existing IP asset's open_ports.
// 语义保持原样：直接走底层连接（不走 scoped 事务）。
func (s *AssetStore) AppendIPPort(ipStr string, port int, serviceName string) error {
	return asset.AppendIPPort(s.db, ipStr, port, serviceName)
}

// AppendIPBoundDomain appends a domain to an existing IP asset's bound_domains.
// 语义保持原样：直接走底层连接（不走 scoped 事务）。
func (s *AssetStore) AppendIPBoundDomain(ipStr, domain string) error {
	return asset.AppendIPBoundDomain(s.db, ipStr, domain)
}

// UpsertSubdomain idempotently inserts or merges a subdomain asset and triggers
// side effects: root_domain upsert + IP bound_domains update.
func (s *AssetStore) UpsertSubdomain(req asset.UpsertSubdomainReq) (id string, err error) {
	if s.tx != nil {
		return asset.UpsertSubdomain(s.tx, assetCompanyResolver{}, req)
	}
	return s.withCompanyScopeMutation(func(scoped *AssetStore) (string, error) {
		return asset.UpsertSubdomain(scoped.tx, assetCompanyResolver{}, req)
	})
}

// UpsertApp idempotently inserts or merges an app asset.
func (s *AssetStore) UpsertApp(req asset.UpsertAppReq) (string, error) {
	if s.tx != nil {
		return asset.UpsertApp(s.tx, assetCompanyResolver{}, req)
	}
	return s.withCompanyScopeMutation(func(scoped *AssetStore) (string, error) {
		return asset.UpsertApp(scoped.tx, assetCompanyResolver{}, req)
	})
}

// UpsertHTTPService inserts or merges an HTTP service asset. Domain, port,
// service_name, and root_domain are auto-extracted from URL.
func (s *AssetStore) UpsertHTTPService(req asset.UpsertHTTPServiceReq) (string, error) {
	if s.tx != nil {
		return asset.UpsertHTTPService(s.tx, assetCompanyResolver{}, req)
	}
	return s.withCompanyScopeMutation(func(scoped *AssetStore) (string, error) {
		return asset.UpsertHTTPService(scoped.tx, assetCompanyResolver{}, req)
	})
}

// UpsertOtherService inserts or merges a non-HTTP service asset.
func (s *AssetStore) UpsertOtherService(req asset.UpsertOtherServiceReq) (string, error) {
	if s.tx != nil {
		return asset.UpsertOtherService(s.tx, assetCompanyResolver{}, req)
	}
	return s.withCompanyScopeMutation(func(scoped *AssetStore) (string, error) {
		return asset.UpsertOtherService(scoped.tx, assetCompanyResolver{}, req)
	})
}

// UpsertEndpoint inserts or merges an endpoint asset. Domain, port, root_domain
// are auto-extracted from the URL.
func (s *AssetStore) UpsertEndpoint(req asset.UpsertEndpointReq) (string, error) {
	if s.tx != nil {
		return asset.UpsertEndpoint(s.tx, assetCompanyResolver{}, req)
	}
	return s.withCompanyScopeMutation(func(scoped *AssetStore) (string, error) {
		return asset.UpsertEndpoint(scoped.tx, assetCompanyResolver{}, req)
	})
}

// =====================================================================
// 查询 / 统计 / 删除转发
// =====================================================================

// QueryByType returns assets rows of a given type, newest first.
func (s *AssetStore) QueryByType(typ string, limit, offset int) ([]*asset.Asset, error) {
	return asset.QueryByType(s.db, typ, limit, offset)
}

// CountByType returns the total number of assets of a type (for server-side pagination).
func (s *AssetStore) CountByType(typ string) (int, error) {
	return asset.CountByType(s.db, typ)
}

// QueryByCompany returns assets for a company, optionally filtered by type.
// limit <= 0 means no limit.
func (s *AssetStore) QueryByCompany(companyID int64, typ string, limit, offset int) ([]*asset.Asset, error) {
	return asset.QueryByCompany(s.db, companyID, typ, limit, offset)
}

func (s *AssetStore) CountByCompany(companyID int64, typ string) (int, error) {
	return asset.CountByCompany(s.db, companyID, typ)
}

// QueryByTask returns assets rows that have a given task_id in task_ids.
// 纯资产查询在 domain/asset；任务源注水（hydrateTaskAssetSources）是任务域
// 逻辑，留在装配层。
func (s *AssetStore) QueryByTask(taskID int64, typ string, limit, offset int) ([]*asset.Asset, error) {
	assets, err := asset.QueryByTask(s.db, taskID, typ, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateTaskAssetSources(taskID, assets); err != nil {
		return nil, err
	}
	return assets, nil
}

func (s *AssetStore) CountByTask(taskID int64, typ string) (int, error) {
	return asset.CountByTask(s.db, taskID, typ)
}

func (s *AssetStore) CountsByTypeForTask(taskID int64) (map[string]int, error) {
	return asset.CountsByTypeForTask(s.db, taskID)
}

// DeleteByTaskID removes assets owned only by taskID and detaches taskID from
// assets shared with other tasks. Full task deletion uses the coordinated
// transaction in DeleteTaskCascadePrepared; this method remains for callers
// that explicitly manage only asset associations.
func (s *AssetStore) DeleteByTaskID(taskID int64) (int64, error) {
	return asset.DeleteByTaskID(s.db, taskID)
}

// HostsByTask returns the exact HTTP host candidates attached to a task's
// assets. Domain/IP columns cover root domains, subdomains and non-HTTP
// services; URL covers HTTP services and endpoints.
func (s *AssetStore) HostsByTask(taskID int64) ([]string, error) {
	return asset.HostsByTask(s.db, taskID)
}

// HostsForTaskDeletion returns hosts that belong to the task being deleted and
// are not referenced by any other live task. Current-task candidates include
// both task_ids ownership and exploration anchors so legacy seeded assets (which
// were anchor-only) are covered. Protection is host-wide: if another live task
// references any asset for a candidate host, that host's global traffic remains.
func (s *AssetStore) HostsForTaskDeletion(taskID, explorationID int64) ([]string, error) {
	return hostsForTaskDeletion(s.db, taskID, explorationID)
}

type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// hostsForTaskDeletion is shared by the read-only AssetStore API and the task
// deletion transaction. Coordinated traffic deletion must call it through the
// transaction path so asset/anchor writes stay locked until the task delete is
// committed.
func hostsForTaskDeletion(q rowsQuerier, taskID, explorationID int64) ([]string, error) {
	rows, err := q.Query(`
WITH current_assets AS (
  SELECT id FROM assets WHERE $1 = ANY(task_ids)
  UNION
  SELECT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes n ON n.id=ea.node_id
  WHERE n.exploration_id=$2
),
other_assets AS (
  SELECT DISTINCT a.id
  FROM assets a
  WHERE EXISTS (
    SELECT 1 FROM tasks t
    WHERE t.id<>$1 AND t.deleted_at IS NULL AND t.id=ANY(a.task_ids)
  ) OR EXISTS (
    SELECT 1
    FROM exploration_anchors ea
    JOIN exploration_nodes n ON n.id=ea.node_id
    JOIN tasks t ON t.exploration_id=n.exploration_id
    WHERE ea.asset_id=a.id AND t.id<>$1 AND t.deleted_at IS NULL
  )
)
SELECT COALESCE(a.domain,''), COALESCE(a.ip,''), COALESCE(a.url,''), true
FROM assets a JOIN current_assets c ON c.id=a.id
UNION ALL
SELECT COALESCE(a.domain,''), COALESCE(a.ip,''), COALESCE(a.url,''), false
FROM assets a JOIN other_assets o ON o.id=a.id`, taskID, explorationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make(map[string]struct{})
	protected := make(map[string]struct{})
	add := func(dst map[string]struct{}, raw string) {
		raw = strings.TrimSpace(strings.ToLower(raw))
		if raw != "" {
			dst[raw] = struct{}{}
		}
	}
	for rows.Next() {
		var domain, ip, rawURL string
		var candidate bool
		if err := rows.Scan(&domain, &ip, &rawURL, &candidate); err != nil {
			return nil, err
		}
		dst := protected
		if candidate {
			dst = candidates
		}
		add(dst, domain)
		add(dst, ip)
		if u, err := url.Parse(rawURL); err == nil {
			add(dst, u.Hostname())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(candidates))
	for host := range candidates {
		if _, shared := protected[host]; !shared {
			out = append(out, host)
		}
	}
	sort.Strings(out)
	return out, nil
}

// GetByIDs returns assets with the given ids (order preserved by id array order).
func (s *AssetStore) GetByIDs(ids []string) ([]*asset.Asset, error) {
	return asset.GetByIDs(s.db, ids)
}

// DeleteByCompanyID hard-deletes all assets belonging to a company. Returns rows deleted.
func (s *AssetStore) DeleteByCompanyID(companyID int64) (int64, error) {
	return asset.DeleteByCompanyID(s.db, companyID)
}

// DeleteByHost hard-deletes every asset whose host exactly matches the given value
// (域名归一化 DomainKey 已上提 core/store)。Returns rows deleted, grouped by type.
func (s *AssetStore) DeleteByHost(host string) (map[string]int64, error) {
	return asset.DeleteByHost(s.db, host)
}

// DeleteByIDs hard-deletes assets by their IDs. Returns the number of rows deleted.
func (s *AssetStore) DeleteByIDs(ids []string) (int64, error) {
	return asset.DeleteByIDs(s.db, ids)
}

// UpdateProjectIDs binds a set of assets to one project (empty clears it).
func (s *AssetStore) UpdateProjectIDs(ids []string, projectID string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var project any
	if strings.TrimSpace(projectID) != "" {
		project = strings.TrimSpace(projectID)
	}
	res, err := s.db.Exec(`UPDATE assets SET project_id=$1, updated_at=now()
		WHERE id = ANY($2::text[])`, project, ids)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountsByType returns asset counts per type.
func (s *AssetStore) CountsByType() (map[string]int, error) {
	return asset.CountsByType(s.db)
}

// =====================================================================
// 私有转发：db 包内遗留调用点（task_scope.go 与单测引用的归一化/序列化工具，
// 实现在 domain/asset，别名统一收口在批 4/5）
// =====================================================================
