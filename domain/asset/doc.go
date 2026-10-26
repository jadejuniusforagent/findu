// Package asset 资产域存储层（批 3 大块组第八刀）。
//
// 表属主：assets（统一资产表，type 分 root_domain / subdomain / ip / app /
// service / endpoint 六形态）与 asset_intercept_rules（全局资产封禁规则，与
// 任务级 task_intercept_rules 同构，值类型复用 domain/task.InterceptRule）。
//
// 三层职责切分：
//   - 本包：单表 CRUD / upsert 家族 / 宽列扫描 / DSL→SQL 编译引擎 / 拦截规则与
//     「先拦截后允许」闸门匹配（纯计算）。
//   - db 装配层（db/assets.go 等）：AssetStore 结构与事务协议（companies 归属
//     解析 lockCompanyScopeMutation 属资产×企业交叉，等 companies 域迁出后随
//     CompanyResolver 一起下沉）、task 源注水（hydrateTaskAssetSources）、
//     scopeTargetCTE 的 InScope 查询、CheckAssetsIntercept 的全局∪任务规则收集、
//     任务删除用的 hostsForTaskDeletion（读 tasks/exploration_anchors）。
//   - 跨域纯工具：DomainKey / RootDomain 在 core/store/netkey.go。
package asset
