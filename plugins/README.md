# plugins/ 插件目录（Step 4 起）

每个插件一个子目录，目录名 = 清单 `id`（小写、可含 `-`，2..64 字符）。Go 包名用
目录名的下划线形式（如 `plugins/c2` → `package c2`）。

## 布局

    plugins/<id>/
      plugin.json          # 必填，符合 contracts/plugin-manifest.schema.json
      plugin.go            # 实现 pluginhost.Plugin（Manifest/Start/Stop/Health）
      migrations/          # 可选，插件自持表的迁移（清单 migrations.dir 指向这里）
      README.md            # 可选

## 硬约束（cmd/layercheck 与 core/pluginhost 双重把关）

1 包内 `Manifest()` 返回的清单必须能被 `pluginhost.ParseManifest`/`Validate` 通过。
2 `owns_tables` 只能声明 `plugin-<id>` 名下的表，唯一事实源是
   `docs/refactor/TABLE-OWNERSHIP.md` 与 `core/ownership/tables.go`；
   core/domain/别的插件的表一律拒绝注册。
3 插件不得 import `finduai/server`、`finduai/db`、`finduai/cmd/*`。
4 插件之间不得直接 import；跨插件协作走 `requires.plugins` 声明 + 事件契约。
5 插件只通过 core 暴露的接口落库，不自建会话/消息类表。

## 状态（2026-10-20，Step 4 批 5）

宿主骨架 `core/pluginhost`、能力接口 `core/provider`（三组 provider 各带内存 mock）与第一个真实插件
`plugins/intel` 已落地。intel 贡献 `/api/vulnkb*`、`/api/vulnintel*` 六条路由，`Stop` 后由宿主返回
503；清单通过 `provides.datasets` 声明数据集，通过 `owns_tables` 声明 `plugin-intel` 四张表。
批 4 已把 `vulnintel` 抓取/富化管线迁入 `plugins/intel/vulnintel`，Start/Stop 由插件驱动；
批 5 已把 `vuln_kb` / `vuln_intel` SQL 迁入 `plugins/intel/vulnstore`，`db` 只保留注入槽位。
卸载演练（`SET SCHEMA archive`）在 `findu_test` 完成，详见 `docs/refactor/STEP4-MIGRATION-MAP.md` §三/§六/§七。

新增插件建议先读 `plugins/intel/`：它是路由插件的最小完整样例（清单内嵌、Deps 注入、生命周期、
路由贡献、单测）。动态加载 .so 尚未实现，插件与宿主同仓编译、在 `server.New` 装配。

## Provider 插件（外部接入槽位）

清单 kind 保留 `provider-c2` / `provider-webshell` / `provider-traffic`，接口在 `core/provider`，
契约测试用 `core/provider/mock`。**核心仓库不提供 C2 / 流量录制的生产实现，也不迁移现役
findu C2 与 findings 流量证据链**；外部实现接入前先读
`docs/refactor/EXTERNAL-PROVIDER-SCOPE.md`。宿主侧通用 `Provider()` 接线与字段级接入规范
随首个外部 provider 一起补。
