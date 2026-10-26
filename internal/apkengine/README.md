# apkengine — 纯 Go 的 APK/DEX 秒级分析引擎

对 ASC(MG1937/Droid ASC,Python)的 Go 重写。设计原则与原项目一致:
**不做预处理、不建索引,把 APK 当只读数据库,按需毫秒级提取与检索。**

## 与 Python 原版的模块对应

| Go 包/文件 | Python 原版 | 状态 |
|---|---|---|
| `leb128.go` | `asc_core/utils/leb128.py` | 已移植 |
| `mutf8.go` | `mutf8` pip 包(DecodeMUTF8/EncodeMUTF8) | 已移植 |
| `dex/` | `asc_core/utils/tinydex.py` + `dex_parser.py` | 待移植 |
| `zipprobe/` | `asc_client/apk_handler.py`(CD 定位 + Deflate 探测) | 待移植 |
| `findrefs/` | `asc_core/findrefs/`(四类 locator + code_item_scan) | 待移植 |
| `decompile/` | `asc_core/utils/decompiler*.py` + `core/dex/*`(重建) | 待移植 |

## 移植约定

1. **语义保真优先于 Go 惯用法**。函数级与 Python 版行为逐一对齐(含边界行为),
   唯有正确性被差分测试证实后才允许偏离。
2. **差分测试是生命线**。`findrefs/testdata/` 内含 10 个真实 APK(drozer 模块
   样本,来源 drozer 3.1.0 wheel,BSD-3)与 `golden_real.json`(原版 Python ASC
   的输出基线,由 `gen_golden.py` 生成);Go 测试逐行比对,当前全部一致。
3. **损坏输入的处理**:Python 版靠 IndexError 崩溃,本引擎底层读取直接索引
   (快速路径不带边界检查),由 `dex.Parse` 顶层 recover 统一转 error。
4. uleb128 快速路径最多消费 5 字节(28 位载荷),与原版一致;DEX 索引不会超出。

## 实证发现(移植过程中确认的原版行为)

- **map_list 语义**:原版 `_build_map_bymap` 把 map 中 `0x2000` 条目的
  (size, off) 当作「class_data 项数 / class_data 起点」使用(在真实 d8 产物
  上已实证可行),并非 DEX 规范的 code_item 语义。Go 按原版行为移植。
- **`_build_map_bydef` 回退路径在原版不可用**(`NameError` 潜在 bug),
  Go 版回退路径实现为正确的 class_defs 扫描(记录在案的偏离:仅当 map
  缺失 0x2000 时触及,原版直接崩溃)。
- **字符串定位的偏移链**:`locate` 依赖「字符串数据区连续布局」,命中项
  返回的是「下一字符串 data_off 反查 idx - 1」;因此字符串表最后一项、
  以及数据区不连续的 DEX 存在不可检索项——quirk 原样保留。
- **查询串是正则不是字面量**:原版把查询串喂给 `re.compile`,`[B` 这类
  数组描述符查询会让 Python 版崩溃。Go 侧为字面量字节匹配(记录在案的
  有意偏离,已在差分集中排除该类查询)。

## 移植顺序

搜索路径先行(zipprobe → dex → findrefs,差分通过即可支撑图形页与 Agent 的
检索类功能),重建 + 反编译最后(支撑源码查看)。

## 当前形态与部署

- **检索(findrefs)/类定位/AXML 解码**:纯 Go,进程内,零外部依赖。
- **反编译(android_source)**:检索定位 Go 原生;DAD 反编译经子进程调
  ASC fork(`python3 main.py getclass ...`),不做 Go 重写。部署两个环境变量:
  - `ANDROID_ASC_PATH`:ASC fork 目录(含 main.py),默认 `external/asc`
  - `ANDROID_ASC_PYTHON`:解释器(需 androguard 4.x + mutf8),默认 `python3`
  未部署时 source 路径返回 503 与部署提示,检索功能不受影响。
- **宿主侧 Python 依赖已移除**(2026-09-14):`scripts/install_asc.sh`、ascenv
  venv 与 ASC 自动更新器都已下线。默认反编译链为**纯 Go**(go-dad → smali 兜底),
  不依赖宿主 Python;需要 DAD 时手动提供上述两个环境变量即可(不做自动安装/更新)。
- ~~自动监控更新~~:已随 Python 依赖一并移除。
  失败自动回滚旧版本。安装的 commit 记录在 `.asc-lock`,状态看
  `GET /api/android/runtime`;GitHub 限频可用 `GITHUB_TOKEN` 提升。
- 服务层 `internal/apkanalysis`:样本归档(sha256 去重,`data/apk_analysis/`)、
  项目绑定、任务落库;HTTP `/api/android/*` + Agent 原生工具
  `android_analyze / android_findrefs / android_source / android_samples`。
