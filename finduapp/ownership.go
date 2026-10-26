package finduapp

// APIOwner 表示一个 /api 前缀在合并命名空间里的归属。
//
// 迁移期只有两种合法归属：
//   - OwnerFindu：整个前缀由 findu 兜底处理。宿主即使注册了同名路由也不会被命中，
//     因此 server 侧的 owner 契约测试要求这些「被遮蔽的宿主前缀」逐条登记。
//   - OwnerDual：宿主只接管明确列出的方法/形状，其余仍交给 findu。当前没有
//     OwnerDual 声明；保留该归属类型，供后续需要部分接管的前缀使用。
//
// 新增前缀必须先在这里声明，再决定是否需要双跑；不允许同一个前缀出现两条声明。
type APIOwner string

const (
	OwnerFindu APIOwner = "findu"
	OwnerDual  APIOwner = "dual"
)

// APIClaim 是一条前缀归属声明。Reason 只写判定依据，不写实现细节，避免文档漂移。
type APIClaim struct {
	Prefix string
	Owner  APIOwner
	Reason string
}

// APIClaims 是 /api 命名空间的唯一归属清单。OwnedAPIPaths 由它派生，
// 路由安装与契约测试都读这一份，防止「清单改了、兜底没改」。
func APIClaims() []APIClaim {
	return []APIClaim{
		{Prefix: "/api/c2", Owner: OwnerFindu, Reason: "C2 仍是外部接入槽位，数据与页面都在 findu"},
	}
}

// OwnedAPIPaths 列出「由 findu 兜底」的 /api 资源前缀。
//
// 判定依据是数据归属，不是代码新旧：这些资源的数据在 PG 的 findu schema 里
// （conversations / vulnerabilities / assets / projects / c2 …），宿主 public
// schema 里的同名表是空的。合并到同一个 /api 命名空间后，这些前缀即使在宿主
// 路由表里有同名注册，也一律交给 findu，避免旧页面读到空表。
//
// 不在列表里的资源（tools / logs / stats / tokens / auth / health / mcp /
// workspace / llm / traffic / reports / vuln-kb / notify / multi-agent …）
// 以宿主实现为准。
func OwnedAPIPaths() []string {
	claims := APIClaims()
	out := make([]string, 0, len(claims))
	for _, c := range claims {
		out = append(out, c.Prefix)
	}
	return out
}
