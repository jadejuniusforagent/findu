# Supervisor 子代理分类

本目录是 supervisor / deep 多代理的 Markdown 子代理定义，由 `config-findu.yaml` 的
`agents_dir: agents` 加载。主代理 `orchestrator-supervisor.md` 只负责分派，不直接承担
专项工作；其余文件是可按任务类别委派的专家代理。

## 分类

| 文件 | id | 职责 | 绑定角色 |
|---|---|---|---|
| `recon.md` | `recon` | 资产、子域、端口、服务、指纹、目录、信息收集 | 渗透测试 |
| `web.md` | `web` | Web/API 漏洞验证、PoC、请求响应证据 | 渗透测试 |
| `exploit.md` | `exploit` | 利用、提权、横向、权限维持、WebShell | 渗透测试 |
| `audit.md` | `audit` | 云/容器/合规/数字取证，只评估不利用 | 审计取证 |
| `report.md` | `report` | 报告、证据链、风险定级、修复建议 | 报告撰写 |
| `retest.md` | `retest` | 原漏洞复测、修复验证、结论更新 | 漏洞复测 |
| `general.md` | `general` | 通用兜底、澄清、轻量任务 | 默认 |

## 约定

- `description` 是 supervisor 的路由标签，写清楚“负责什么、不负责什么”。
- 已按类别收窄 `tools`：`recon` 只保留侦察/资产类，`web` 保留 Web/API 验证类，
  `exploit` 保留利用/后渗透类，`audit` 保留审计/取证类，`report` 保留证据读取类，
  `retest` 保留复测类；`general` 不写 `tools`，保留全工具兜底。
- 注意：`role_tools` 只过滤 MCP/配方工具；`read_file` / `write_file` / `execute` 等
  Eino 文件系统内置工具由 `multi_agent.eino_skills.filesystem_tools` 全局控制，目前
  无法按子代理单独关闭。`audit` 的只读边界由提示词约束，不是硬隔离。
- 子代理共用宿主当前 LLM profile；当前配置不支持每个子代理单独指定模型。
- 修改 Markdown 后需要重启容器（当前没有配置热加载）。
- `README.md` 会被加载器跳过，不会成为子代理。
