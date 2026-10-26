# intel 插件（第一个真实插件）

漏洞知识库（1day）、自动抓取的漏洞情报与指纹规则的**入口与生命周期**插件。
Step 4 批 3 落地；kind=`dataset`，`owns_tables` 为 `plugin-intel` 名下四张表：

    fingerprint_rules
    fingerprint_sources
    vuln_kb
    vuln_intel

## 做了什么

- 六条路由从 `server` 搬进插件：
  `GET /api/vulnkb`、`GET /api/vulnkb/stats`、`GET /api/vulnkb/{id}`、
  `GET /api/vulnintel`、`POST /api/vulnintel/refresh`、`GET /api/vulnintel/status`。
- 插件实现 `pluginhost.Plugin`（清单/Start/Stop/Health）与 `pluginhost.RouteProvider`。
- 首次 `Start` 触发一次幂等后台任务：指纹表建表 + 24h 同步、`vulnkb/` 目录导入。
- `Stop` 后六条路由由宿主返回 **503**；重启恢复，且不重复导入目录。
- **批 4**：`vulnintel` 抓取/富化管线搬到 `plugins/intel/vulnintel`，插件
  `Start`/`Stop` 管理轮询生命周期；`server.Manager` 不再持有该管线。
- **批 5**：`vuln_kb` / `vuln_intel` SQL 搬到 `plugins/intel/vulnstore`；`domain/finding`
  只保留类型，`db` 只保留 `VulnKBStore` 注入槽位。
- 卸载演练：`SET SCHEMA archive` 搬表 / 搬回，已在 `findu_test` 验证（见迁移图 §三）。

## 没做什么（下一批）

- 指纹**匹配**仍在 `fingerprint` 包（`enrich` 直接调用），本批只迁规则同步的启动入口。

## 依赖注入

```go
type Deps struct {
    Store     Store         // vulnstore.Store：漏洞库/情报读取
    Bootstrap func()        // 首次 Start：指纹同步 + vulnkb 目录导入
    Intel     IntelPipeline // vulnintel.Manager；Start/Stop 由插件驱动
}
```

插件不 import `finduai/db` / `finduai/server`；装配在 `server/intel_plugin.go`。

## 验证

```bash
go test -count=1 ./plugins/intel/ -cover
go test -count=1 ./server/ -run 'TestIntelPluginRoutesLifecycle|TestPluginsEndpoint'
go run ./cmd/layercheck
python3 scripts/uninstall_plugin_data.py --plugin intel --db findu_test --apply --yes
python3 scripts/uninstall_plugin_data.py --plugin intel --db findu_test --restore --apply --yes
```
