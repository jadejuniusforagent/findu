<div align="center">

# FindUAI

AI 自主渗透测试平台

</div>

---

## 系统环境

**Windows 用户推荐用 WSL2（Ubuntu）部署**。

| 组件 | 要求 | 用途 |
| --- | --- | --- |
| 操作系统 | Linux（含 WSL2）/ macOS | Windows 经 WSL2（Ubuntu 22.04+） |
| Docker | Docker Engine + Compose v2 | Docker 部署；`install.sh` 可自动检测 / 安装（Linux） |
| Go | ≥ 1.26 | 本地编译；Linux/WSL 另需 `gcc` |
| Node.js | ≥ 20 | 前端静态构建 |
| PostgreSQL | 16 | Compose 自带 `postgres:16-alpine`；连已有库需 16 及以上 |
| LLM | `ANTHROPIC_API_KEY` 或 `OPENAI_API_KEY` | 探索功能需要；也可首次启动后在 UI 里配置 |

端口默认：**Docker 模式宿主机 `8081`**；**本地编译模式 `8787`**（`./start.sh -addr :PORT` 可改）；流量录制代理 `8788`（仅绑 127.0.0.1）；PG `5432` 只在 compose 内部网络，不对外发布。

## 安装

> 依赖数据库 **PostgreSQL**；探索需配置 **LLM**（`ANTHROPIC_API_KEY` 或 `OPENAI_API_KEY`，也可在 UI 里配）。

### 方式一：一键安装脚本（推荐）

```bash
cd <项目根目录>
./install.sh
```

脚本会：检测 / 自动安装 Docker → 让你选 **① 全部 Docker** 或 **② 本地编译运行**：

- **① 全部 Docker**：填一个 Postgres 密码（可回车随机）→ 自动写 `.env` → `docker compose up -d`。
- **② 本地运行**：选数据库（连已有 / 用 Docker 起一个）→ 生成 `config.json` → `go` 编译内嵌单二进制 → 启动。

装好后打开 **http://localhost:8081**（Docker 模式；首次进入 `/setup` 设置管理员密码。本地编译模式默认端口为 `8787`）。

### 方式二：Docker Compose（手动）

```bash
cd <项目根目录>
cp .env.example .env          # 填 POSTGRES_PASSWORD、可选 ANTHROPIC_API_KEY
docker build -f Dockerfile.deploy -t findu:local .   # 先构建本地镜像
docker compose up -d          # 本地镜像 findu:local + postgres
# → http://localhost:8081
```

> 请用 `start.sh` / `start.bat` 启动，而不是直接跑 `./findu`。它是个守护脚本：程序退出后按退出码决定是否重新拉起。
> 后台常驻：`nohup ./start.sh >findu.log 2>&1 &`。

### 方式三：从源码编译单二进制

```bash
# 1) 前端静态导出
cd web && npm ci && npm run build:static && cd ..
# 2) 拷进内嵌目录
cp -r web/out server/webui/dist
# 3) 编译（-tags embedui 才内嵌前端；需 cgo，Linux/WSL 先装 gcc）
CGO_ENABLED=1 go build -tags embedui -o findu ./cmd/findu
./start.sh
```

### 方式四：构建跨平台 Release 压缩包

```bash
./build.sh --release
# 产物：dist/findu-<版本号>-*.zip
```

---

## 更新升级

> 升级只换程序、不动数据：Postgres 数据卷 `pgdata`、`./data`、`./skills` 都会保留。**数据库迁移无需手动执行**——`findu` 每次启动会幂等重跑 `schema.sql`，即"重启即迁移"。升级前仍建议先备份 `./data` 与数据库。

### 方式一：页面一键更新（推荐）

在 **系统配置** 页的**版本与更新**卡片里，可以直接检查并安装新版本，无需登录服务器。

- **失败不会留下坏程序**：校验或冒烟不通过就丢弃暂存件、继续跑当前版本；换装后的新版若连续 3 次启动失败，会自动回滚到上一版本。
- **随时可回退**：上一版本保留为 `findu.old`，卡片上有「回滚到上一版本」。注意数据库结构不会回退。
- **更新会中断正在运行的任务**——更新即重启，请在空闲时进行。

### 方式二：一键更新脚本

```bash
cd <项目根目录>
./update.sh
```

### 方式三：Docker Compose（手动）

```bash
cd <项目根目录>
docker build -f Dockerfile.deploy -t findu:local .   # 重建本地镜像
docker compose up -d --force-recreate findu          # 换新镜像重启
docker image prune -f          # 清理旧镜像（可选）
```

---

## 配置

**数据库**（`config.json`，或用环境变量 `FINDU_PG_DSN` 覆盖）：

```json
{
  "database": {
    "host": "127.0.0.1", "port": 5432,
    "user": "findu", "password": "yourpass",
    "dbname": "findu", "sslmode": "disable"
  }
}
```

**LLM**：`export ANTHROPIC_API_KEY=sk-...`（或 `OPENAI_API_KEY`），也可在 UI 的「LLM 配置」页填写。
可选：`FINDU_LLM_PROVIDER` / `FINDU_LLM_MODEL` / `FINDU_LLM_BASE_URL` / `FINDU_LLM_PROXY`。

**并发**：每个任务的 work agent 数在「系统设置」里配置（默认 3）。

**常用参数**：`./start.sh -addr :8787 -proxy :8788`（`-addr` 前端+API，`-proxy` 流量录制代理）。启动脚本会把参数原样透传给 `findu`。

### 反向代理部署（HTTPS / 只开放 443）

前端和 API/SSE 都由同一个后端端口提供，实时活动流默认走**同源**地址，公网只开放 443、把后端端口留内网即可。

SSE 是长连接 + 持续推送，反代**必须关闭缓冲**。Nginx 示例：

```nginx
server {
    listen 443 ssl;
    server_name your.domain.com;
    # ssl_certificate / ssl_certificate_key ...

    location / {
        proxy_pass http://127.0.0.1:8787;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;

        # SSE 关键项：关缓冲、长超时、HTTP/1.1
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
    }
}
```

---

## 许可与免责声明

### 开源协议

本项目采用 **GNU Affero General Public License v3.0（AGPL-3.0）** 授权，完整条款见仓库根目录的 [LICENSE](LICENSE) 文件。

这意味着任何人都可以自由使用、修改和分发本项目，但**衍生作品必须同样以 AGPL-3.0 开源**；特别地，**若你修改本项目并通过网络（如部署为在线服务）向用户提供，也必须向这些用户公开对应的完整源码**。

> ⚠️ **重要提示**：开源协议本身不限制软件的使用用途。以下的「使用限制」与「免责声明」是作者对使用者的额外约定与郑重声明，请务必遵守。

**FindUAI 仅供个人学习、代码研究与本地技术验证使用，不得用于对任何线上系统或网站发起实际测试。**

### 允许使用范围

- 仅可用于**阅读、学习与研究本项目源码**，以及在**本地隔离环境**中进行技术原理验证；
- 适用于个人学习、学术研究、代码审阅等非攻击性用途。

### 禁止事项

- **严禁使用本工具对任何网站、线上服务或联网系统发起扫描、探测、利用或攻击**（无论是否获得授权、是否为自有资产）；
- 严禁将本工具用于任何实际的渗透测试、攻防对抗或生产环境；
- 严禁将本工具用于非法入侵、数据窃取、勒索、拒绝服务或任何破坏性、犯罪性活动；
- 严禁利用本工具从事违反所在国家/地区法律法规的行为。

### 合规责任

使用者须自行遵守所在国家/地区关于网络安全、数据保护与计算机犯罪的全部法律法规（在中国大陆包括但不限于《网络安全法》《数据安全法》《个人信息保护法》及相关司法解释）。**因使用本工具产生的一切法律责任与后果，均由使用者自行承担。**

### 免责声明

本项目按"现状（AS IS）"提供，不附带任何明示或默示的担保。作者及贡献者不对使用本工具（无论使用方式是否得当）所导致的任何直接或间接损失、数据丢失、系统损坏或法律纠纷承担责任。**下载、安装或使用本项目，即表示你已阅读、理解并同意上述全部条款。**
