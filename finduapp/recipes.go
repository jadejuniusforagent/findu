package finduapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"

	"finduai/internal/config"
	"finduai/internal/security"
	actool "finduai/tools"

	"go.uber.org/zap"
)

// 本文件把 findu 的 YAML 工具配方（configs/toolrecipes，89 个）适配成
// CoreTool，让 planner / worker / main 能直接调用 nmap、nuclei、ffuf 这类
// 现成配方，而不是每次都用 Bash 手搓命令行。
//
// 三条设计约束：
//  1. 不重写参数规则 —— 命令构造与执行全部走 findu 的 security.Executor，
//     只是把 (Cfg, Schema, Call) 三个面暴露成 CoreTool 接口。
//  2. 过滤优先于全量 —— 只接 enabled 且二进制真实存在的配方；internal: 前缀的
//     findu 内部工具依赖它自己的 mcp.Server，跳过。
//  3. schema 延迟披露 —— 返回的 names 交给宿主放进 <available-deferred-tools>，
//     模型先用 SearchExtraTools 取 schema 再 ExecuteExtraTool 调用。90 个完整
//     schema 全量进 prompt 会直接吃掉几万 token。

var (
	recipeOnce  sync.Once
	recipeTools []actool.CoreTool
	recipeNames []string
	recipeErr   error
)

// RecipeTools 返回 findu 配方适配出的 CoreTool 列表（进程内只加载一次）。
// names 与 tools 一一对应，供宿主做延迟披露。
func RecipeTools() ([]actool.CoreTool, []string, error) {
	recipeOnce.Do(loadRecipeTools)
	return recipeTools, recipeNames, recipeErr
}

func loadRecipeTools() {
	cp := configPath()
	cfg, err := config.Load(cp)
	if err != nil {
		recipeErr = fmt.Errorf("加载 findu 配置 %s: %w", cp, err)
		log.Printf("[recipes] %v", recipeErr)
		return
	}
	if strings.TrimSpace(cfg.Security.ToolsDir) == "" {
		log.Printf("[recipes] findu 配置未设置 security.tools_dir，跳过配方注册")
		return
	}
	ex := security.NewExecutor(&cfg.Security, nil, zap.NewNop())

	var skippedMissing, skippedInternal int
	for _, name := range ex.RecipeNames() {
		tc, ok := ex.RecipeConfig(name)
		if !ok {
			continue
		}
		if strings.HasPrefix(tc.Command, "internal:") {
			skippedInternal++
			continue
		}
		// 二进制不存在就不注册：注册了模型会调，调了就是 command not found，
		// 白烧一轮上下文。镜像里装没装由 Dockerfile 决定，这里如实反映。
		if _, err := exec.LookPath(tc.Command); err != nil {
			skippedMissing++
			continue
		}
		schema := ex.RecipeInputSchema(name)
		if schema == nil || len(schema) == 0 {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		recipeTools = append(recipeTools, newRecipeTool(ex, tc, schema))
		recipeNames = append(recipeNames, name)
	}
	log.Printf("[recipes] findu 工具配方接入 %d 个（跳过: 二进制缺失 %d / internal %d）: %s",
		len(recipeNames), skippedMissing, skippedInternal, strings.Join(recipeNames, ","))
}

// gateTargets 从配方配置解析出目标提取声明，供 guard 的逐目标门禁使用。三级规则：
//  1. YAML 显式 targets 声明（推荐，与 tool contract 同构）
//  2. 无声明时按参数名推断：名字落在常见目标键（target/host/ip/domain/url…）里
//     的参数自动成为目标字段——kind 由名字二次确认（ip→ip，url→url，其余 domain）
//  3. 两级都没有 → 运行时按 CommonTargetFields 兜底扫描（宽松，仅影子证据）
func gateTargets(tc *config.ToolConfig) []actool.TargetDecl {
	if len(tc.TargetDecls) > 0 {
		decls := make([]actool.TargetDecl, 0, len(tc.TargetDecls))
		for _, d := range tc.TargetDecls {
			if strings.TrimSpace(d.Field) == "" {
				continue
			}
			decls = append(decls, actool.TargetDecl{Field: strings.TrimSpace(d.Field), Kind: d.Kind})
		}
		if len(decls) > 0 {
			return decls
		}
	}
	var decls []actool.TargetDecl
	seen := map[string]bool{}
	for _, p := range tc.Parameters {
		n := strings.ToLower(strings.TrimSpace(p.Name))
		if seen[n] || !commonTargetFieldNames[n] {
			continue
		}
		seen[n] = true
		decls = append(decls, actool.TargetDecl{Field: p.Name, Kind: targetKindOfName(n)})
	}
	return decls
}

// commonTargetFieldNames 供声明兜底用的参数名集合（小写）。
var commonTargetFieldNames = map[string]bool{
	"target": true, "targets": true, "host": true, "hosts": true,
	"ip": true, "ips": true, "domain": true, "domains": true,
	"url": true, "urls": true, "endpoint": true,
}

// targetKindOfName 从参数名二次确认目标 kind。
func targetKindOfName(name string) string {
	switch {
	case name == "ip" || name == "ips":
		return string(actool.TargetKindIP)
	case name == "url" || name == "urls":
		return string(actool.TargetKindURL)
	case name == "target" || name == "targets" || name == "endpoint":
		return "" // 万能键：类型不定，提取时靠 ParseTarget 自动分类
	default:
		return string(actool.TargetKindDomain)
	}
}

// recipeTargeted wraps a CoreTool for the gate's optional extension interfaces:
// the recipe program runs as a CLI net tool that cannot rewrite the extract
// logic, so the declarations fall back to per-config data-driven extraction.
type recipeTargeted struct {
	actool.CoreTool
	sideEffect string
	decls      []actool.TargetDecl
}

func (r *recipeTargeted) SideEffect() string {
	if r.sideEffect == "" {
		// CLI recipes touch the network by default: all of them will hit external targets. Align with the contract's network enum value.
		return actool.EffectNetwork
	}
	return r.sideEffect
}

func (r *recipeTargeted) ExtractTargets(input json.RawMessage) ([]actool.Target, error) {
	if len(r.decls) > 0 {
		return actool.ExtractTargetsFromInput(input, r.decls)
	}
	return actool.ExtractTargetsFromCommonFields(input)
}

// newRecipeTool 把一个配方包成 CoreTool。Permissions 返回 Allowed 与其它
// host 工具一致——审批/拦截由宿主既有的 guard 与 HITL 层负责，这里不重复设卡。
// 目标声明与副作用自声明挂在 recipeTargeted 包装上，供 guard 的 scope/限速/
// side_effect 门禁探测（可选接口，不进 CoreTool，避免破坏接口实现方）。
func newRecipeTool(exec *security.Executor, tc *config.ToolConfig, schema map[string]any) actool.CoreTool {
	name := tc.Name
	inner := actool.Build(actool.Spec{
		Name:        name,
		Description: recipeDescription(tc),
		Schema:      schema,
		ReadOnly:    func(json.RawMessage) bool { return false },
		Concurrent:  func(json.RawMessage) bool { return false },
		Permissions: func(context.Context, json.RawMessage, actool.Context) actool.Decision {
			return actool.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			args := map[string]interface{}{}
			if len(in) > 0 {
				if err := json.Unmarshal(in, &args); err != nil {
					return actool.Errorf(fmt.Sprintf("%s 参数不是合法 JSON 对象: %v", name, err)), nil
				}
			}
			res, err := exec.ExecuteTool(ctx, name, args)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("%s 执行失败: %v", name, err)), nil
			}
			var b strings.Builder
			isErr := false
			if res != nil {
				for _, c := range res.Content {
					b.WriteString(c.Text)
				}
				isErr = res.IsError
			}
			out := b.String()
			if strings.TrimSpace(out) == "" {
				out = fmt.Sprintf("%s 执行完成，无输出", name)
			}
			if isErr {
				return actool.Errorf(out), nil
			}
			return actool.Text(out), nil
		},
	})
	return &recipeTargeted{
		CoreTool:   inner,
		sideEffect: strings.TrimSpace(tc.SideEffect),
		decls:      gateTargets(tc),
	}
}

// recipeDescription 优先用 short_description（省 token），退化到 description 首段。
func recipeDescription(tc *config.ToolConfig) string {
	if s := strings.TrimSpace(tc.ShortDescription); s != "" {
		return s
	}
	desc := strings.TrimSpace(tc.Description)
	if desc == "" {
		return tc.Name + "（findu 工具配方）"
	}
	if i := strings.IndexAny(desc, "\n"); i > 0 {
		desc = strings.TrimSpace(desc[:i])
	}
	r := []rune(desc)
	if len(r) > 240 {
		desc = string(r[:240]) + "…"
	}
	return desc
}
