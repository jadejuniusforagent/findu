---
name: env-network-and-tooling
description: >-
  环境网络与工具安装铁律:装包/下载优先国内镜像(本机已配好);pip 禁止用 -i 覆盖镜像源;GitHub/rustup/rubygems 等国外源走本机代理(WSL 网关:7897,先探测可用性);单条命令超时上限 300 秒;同一包最多重试 2 次;下载前先测速。Use when installing packages or tools, pulling from GitHub/PyPI/npm, or any command that hangs waiting on network.
metadata:
  tags: [基础设施, 环境, 工具安装, 代理]
mcps: [install-python-package, execute-python-script]
---

## 环境网络与工具安装（装东西前先读这一节）

```
🔴 铁律0: 装包/下载慢 = 源不对,不是网不行。先换源,再谈代理。
🔴 铁律1: 单条 exec 超过 300 秒就要怀疑卡住 —— 别等 1200 秒 inactivity timeout 白烧时间。
🔴 铁律2: 同一个包失败后最多换一种方式重试 1 次(累计 2 次),仍失败 → 记录 "X 装不上" 并跳过,继续下一个。禁止反复重试。

① 镜像源(首选,不需要代理,速度最快) —— 本机已配好,直接用默认命令即可:
  pip3 install X          # 已配清华+阿里镜像,默认就走镜像 ✅
  pip3 install X          # 🚫 禁止再写 -i https://pypi.org/simple —— 那会覆盖镜像,实测单次卡死 1200 秒
  npm i X                 # 已配 registry.npmmirror.com ✅
  apt-get install X       # 已配清华源 ✅
  # 以下未配,需要时按需加:
  go install X            # export GOPROXY=https://goproxy.cn,direct
  cargo install X         # 在 ~/.cargo/config.toml 配 [source.crates-io] replace-with 国内镜像
  gem install X           # gem sources --add https://mirrors.tuna.tsinghua.edu.cn/rubygems/ --remove https://rubygems.org/

② 代理(代理工具 跑在 Windows 宿主,监听 7897;WSL 里走默认网关):
  取网关(不要硬编码,网关可能变): GW=$(ip route | awk '/default/{print $3}')
  探测可用性(必做 —— 代理不一定开着,没开就要回退):
    curl -s -m 5 -x http://$(ip route | awk '/default/{print $3}'):7897 -o /dev/null -w "%{http_code}" https://www.gstatic.com/generate_204
    # 返回 204 = 代理可用;000/超时 = 不可用,别硬走
  按工具内联带上代理(每条命令自己带,不要指望环境变量持久):
    curl -x http://GW:7897 -fL -o out.tgz URL
    git -c http.proxy=http://GW:7897 clone https://github.com/owner/repo
    pip3 install --proxy http://GW:7897 X            # 仅当镜像里确实没有该包
    pipx install --pip-args="--proxy http://GW:7897" X
    wget -e use_proxy=yes -e http_proxy=http://GW:7897 URL
    export http_proxy=http://GW:7897 https_proxy=http://GW:7897   # 仅限单条命令内,用完即弃

③ 回退顺序(严格按序,不要跳步):
  国内镜像 → 直连 → 走代理 → 放弃并如实记录

④ 大文件/长任务(预计 >1 分钟):
  后台跑,不要占住前台: nohup <cmd> > /tmp/x.log 2>&1 &
  断点续传: curl -C - -o out URL   /   aria2c -c -x16 -s16 URL
  下载前先测速: 下 10 秒看速率;低于 300KB/s 就换通道(镜像→代理)
  例:1.36G 的包,直连 155KB/s(150分钟) vs 走代理 29.6MB/s(45秒) —— 差距近百倍

⑤ 装完必须验证,别只看 exit code:
  command -v X && X --version
  缺失就如实记 MISS,不要反复装同一个。

🚨 实测教训(2026-10-01,同一会话内累计烧掉 3 小时):
  pip3 install -i https://pypi.org/simple "git+.../pacu.git"     → 1200 秒 inactivity timeout
  curl https://sh.rustup.rs | sh; cargo install rustscan          → 1200 秒 inactivity timeout
  pipx install "git+.../NetExec" 反复试 4 次(906s失败/313s/638s/305s失败) → 白烧约 36 分钟
  gem install wpscan → 399 秒; curl trivy 安装脚本 → 352 秒
  同期用国内镜像可达的包,单次只要几秒到几十秒。
  结论:这五条全部违反铁律0/1/2,是本会话耗时的主因。
```
