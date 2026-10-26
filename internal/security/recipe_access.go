package security

import "finduai/internal/config"

// 本文件只做「只读访问器」：把 findu 配方工具的元数据暴露给宿主（finduapp），
// 用于把 YAML 配方适配成 CoreTool。
//
// 刻意不改命令构造与执行路径：参数规则（flag / positional / combined /
// template / additional_args）全部复用 executor.go 里已有的实现，这里只提供
// 「看清楚有哪些配方、每个配方要什么入参」的窗口，避免两套参数规则漂移。

// RecipeNames 返回已启用的配方工具名，顺序与配置里的 tools 列表一致。
func (e *Executor) RecipeNames() []string {
	if e == nil || e.config == nil {
		return nil
	}
	out := make([]string, 0, len(e.config.Tools))
	for i := range e.config.Tools {
		if e.config.Tools[i].Enabled {
			out = append(out, e.config.Tools[i].Name)
		}
	}
	return out
}

// RecipeConfig 返回单个已启用配方的配置（未启用或不存在时 ok=false）。
func (e *Executor) RecipeConfig(name string) (*config.ToolConfig, bool) {
	if e == nil || e.toolIndex == nil {
		return nil, false
	}
	tc, ok := e.toolIndex[name]
	return tc, ok
}

// RecipeInputSchema 返回配方的 JSON-Schema，复用 buildInputSchema 的同一份规则。
func (e *Executor) RecipeInputSchema(name string) map[string]interface{} {
	tc, ok := e.RecipeConfig(name)
	if !ok {
		return nil
	}
	return e.buildInputSchema(tc)
}
