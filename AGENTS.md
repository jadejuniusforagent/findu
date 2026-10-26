# AGENTS.md — 推送前安全检查规范

> **每次 git push 前必须逐项执行以下检查，不可跳过。**
> 本文件由 opencode 自动读取，作为项目级持久记忆。

## 1. 敏感信息扫描（必做）

推送前在项目根目录执行全量扫描，确认以下所有维度零残留：

```bash
# 一键扫描脚本（复制到 .temp/ 执行）
git grep -noE 'sk-[a-zA-Z0-9]{20,}' -- | grep -v 'sk-test\|sk-example\|sk-your\|sk-placeholder'
git grep -noE 'github_pat_[a-zA-Z0-9_]{20,}|ghp_[a-zA-Z0-9]{30,}' --
git grep -noE 'Bearer [a-zA-Z0-9._-]{20,}' -- | grep -v 'Bearer <\|Bearer your\|Bearer \$'
git grep -noiE '(password|secret|api_key|apikey)\s*[=:]\s*["\x27][^"\x27]{6,}' -- | grep -viE 'your|example|placeholder|xxx|test|change|\$\{|<|env'
git grep -noE 'wx[0-9a-f]{16}' -- | grep -v 'wx1234567890abcdef\|wx0000000000000000'
git grep -noE 'pan\.quark\.cn/s/|pan\.baidu\.com/s/' --
git grep -noE '[a-z0-9]+\.(dnslog\.cn|ceye\.io|burpcollaborator\.net)' -- | grep -v 'example\|test\|attacker'
git grep -noE 'eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}' -- | grep -v 'example\|test\|demo'
git grep -c '旧项目名标识' -- | grep -v vendor  # 替换为实际要扫描的旧名
git ls-files '.env' '**/.env' | grep -v '.env.example'
git ls-files 'config-findu.yaml' | grep -v template
git ls-files 'keys/' '**/*.key' '*.pem'
git ls-files 'screenshots/*.png'
```

### 扫描维度清单

| 维度 | 正则/规则 | 白名单（允许保留） |
|---|---|---|
| API Key (sk-) | `sk-[a-zA-Z0-9]{20,}` | `sk-test`/`sk-example`/`sk-your`/`sk-placeholder` |
| GitHub Token | `github_pat_`/`ghp_`/`gho_`/`ghs_` | `ghp_1234567890abcdef...`（测试占位符） |
| Bearer Token | `Bearer [token]` | `Bearer <token>`/`Bearer ${VAR}` |
| 密码赋值 | `password/secret/api_key = "value"` | `your`/`example`/`placeholder`/`${VAR}` |
| 微信 AppID | `wx[0-9a-f]{16}` | `wx1234567890abcdef`/`wxabcdef1234567890` |
| 网盘链接 | `pan.quark.cn/s/`/`pan.baidu.com/s/` | 无（全部禁止） |
| dnslog/OOB | `*.dnslog.cn`/`*.ceye.io` | `example.dnslog`/`attacker.dnslog`/`test.dnslog` |
| JWT | `eyJ...` | jwt.io 标准示例 token |
| 旧项目名 | 扫描所有可能暴露前身名称的标识（见下方说明） | vendor 第三方库中的巧合子串 |
| 真实人名/公司 | 真实用户名/开发者ID/公司名（见下方说明） | 无 |
| 部署环境 | 代理工具名/宿主机路径/WSL 代理细节 | 无 |
| .env 文件 | `git ls-files '.env'` | `.env.example`/`.env.template` |
| config-findu.yaml | `git ls-files` | `config-findu.yaml.template` |
| keys/ 目录 | `git ls-files 'keys/'` | 无 |
| screenshots PNG | `git ls-files 'screenshots/*.png'` | 无（截图含真实会话数据，禁止跟踪） |

### 指纹库/漏洞库数据说明

以下目录包含**公开开源指纹数据**（dddd/EHole/wappalyzer 等），其中的 IP/域名/邮箱是指纹规则的一部分，**不是用户自己的敏感数据**，允许保留：
- `fingerprint/snapshots/` — 指纹规则库
- `skills/*/` — 安全测试技能文档中的示例
- 测试文件中的假凭据样本（`ghp_1234567890...`/`AKIAIOSFODNN7EXAMPLE` 等）— 用于测试扫描器检测能力

## 2. 提交信息规范

- **禁止在提交信息中暴露**：旧项目名、改名历史、项目分层结构、来源关系、内部决策
- **提交信息保持中性**：如 "FindUAI 初始版本"、"修复 xxx 功能"、"更新文档"
- **禁止描述性提交信息**：如 "全量改名"、"移植"、"合并子系统" 等暴露来源的措辞

## 3. 历史清理规范

- 推送前确认 `git log --oneline` 不包含暴露性提交信息
- 如果历史中有敏感提交信息，用 `git commit --amend` 或 `git rebase` 清理后再推送
- 公开仓库保持**单个初始提交**，不保留开发历史
- force-push 前确认本地 `git log --all -S '敏感词'` 为 0

## 4. GitHub Actions 日志安全

- 工作流中禁止 `echo $SECRET` 或打印环境变量值
- 确认 `secrets.GITHUB_TOKEN` 不被显式输出
- `build.sh` 输出不包含 API key/token（构建脚本不应读取真实凭据）
- Release notes 不包含内部决策/改名历史（CHANGELOG.md 已删除，release notes 从简）

## 5. .gitignore 必须包含

```
.env
config-findu.yaml
keys/
screenshots/*.png
.temp/
vulnkb/
data/
```

## 6. 推送前最终检查清单

```bash
# 1. 确认提交信息中性
git log --oneline

# 2. 确认无敏感文件被跟踪
git ls-files '.env' 'config-findu.yaml' 'keys/' 'screenshots/*.png' '.temp/'

# 3. 确认无旧项目名残留
git grep -c '旧项目名标识' -- | grep -v vendor  # 替换为实际要扫描的旧名

# 4. 确认 .gitignore 覆盖敏感路径
grep -E '\.env|config-findu|keys/|screenshots|\.temp|vulnkb' .gitignore

# 5. go build 通过
go build ./...

# 6. 推送（走代理）
# TOKEN=$(tr -d '\r' < /mnt/c/Users/sakura/AppData/Local/hermes/.env | grep '^GITHUB_TOKEN=' | cut -d= -f2-)
# HOSTIP=$(ip route show default | awk '{print $3}')
# git config http.proxy "http://$HOSTIP:7897"
# git push origin main
# git config --unset http.proxy
```
