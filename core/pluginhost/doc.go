// Package pluginhost 插件宿主：注册表、生命周期、能力校验、路由贡献与卸载语义。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
// 校验：cmd/layercheck（CI 闸门）。
//
// 已落地（Step 4 批 1-3）：
//
//	1 清单：contracts/plugin-manifest.schema.json 的 Go 映射 + 严格解析
//	2 注册表：id 唯一、API 版本一致、表归属必须与 core/ownership 矩阵一致
//	3 生命周期：registered -> starting -> started -> stopping -> stopped/failed，
//	   启动前检查 requires.plugins 已 started，停止时拒绝「仍被依赖」
//	4 健康：只有 started 的插件可探活
//	5 路由：可选 RouteProvider 把 "METHOD /path" -> handler 交给宿主统一注册；
//	   插件停止后由宿主返回 503（入口还在、实现下线）
//
// 插件目录布局见 plugins/README.md；能力接口见 core/provider；第一个真实插件
// 是 plugins/intel。本包不加载磁盘上的 .so（同仓编译，装配在 server.New 完成）。
package pluginhost
