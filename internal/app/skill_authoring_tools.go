package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"finduai/internal/config"
	"finduai/internal/mcp"
	"finduai/internal/mcp/builtin"
	"finduai/internal/skillpackage"

	"go.uber.org/zap"
)

// 技能自扩展工具（P2 第二批）。
//
// 目标：让 agent 在会话中直接沉淀/修订技能包，而不是只能人工走 HTTP 接口。
// 对应 FINDU 的 create_skill / update_skill_file，但落盘口径与 FindUAI 的 HTTP 侧一致：
//
//	<skills_dir>/<name>/SKILL.md  + 包内附件
//
// 安全约束（写盘类工具，全部硬校验）：
//   - 技能名：小写字母/数字/连字符，≤100，与目录名一致
//   - 附件相对路径：Clean 后必须仍在技能目录内（禁止 ../ 越出）
//   - 单文件体积上限
//   - create 拒绝覆盖已存在的技能；update 拒绝操作不存在的技能
//   - 由 agent.skill_authoring 开关控制（默认开启；置 false 可整体关闭）
const (
	skillAuthoringMaxFileBytes = 256 * 1024
	skillAuthoringMaxNameLen   = 100
)

// skillsRootForAuthoring 解析技能根目录。
// 口径与 Eino 侧 prepareEinoAgenticSkills 的 filepath.Abs(cfg.SkillsDir) 一致
// （服务由 wsl-start.sh 在配置目录下启动，故与 HTTP 侧的「配置目录相对」结果相同）。
func skillsRootForAuthoring(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("配置为空")
	}
	dir := strings.TrimSpace(cfg.SkillsDir)
	if dir == "" {
		dir = "skills"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("skills_dir 解析失败: %w", err)
	}
	return abs, nil
}

// validAuthoringSkillName 与 HTTP 侧 isValidSkillName 同规则。
func validAuthoringSkillName(name string) bool {
	if name == "" || len(name) > skillAuthoringMaxNameLen {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

// resolveSkillFilePath 把相对路径安全地接到技能目录下；越界直接报错。
func resolveSkillFilePath(root, name, rel string) (string, error) {
	dir := filepath.Join(root, name)
	clean := filepath.Clean(strings.TrimSpace(rel))
	if clean == "" || clean == "." {
		clean = "SKILL.md"
	}
	full := filepath.Join(dir, clean)
	relCheck, err := filepath.Rel(dir, full)
	if err != nil {
		return "", fmt.Errorf("路径解析失败: %w", err)
	}
	if relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) || filepath.IsAbs(relCheck) {
		return "", fmt.Errorf("非法路径（越出技能目录）: %s", rel)
	}
	return full, nil
}

// registerSkillAuthoringTools 注册 create_skill / update_skill_file 两个工具。
func registerSkillAuthoringTools(mcpServer *mcp.Server, cfg *config.Config, logger *zap.Logger) {
	if mcpServer == nil || cfg == nil {
		return
	}
	if !cfg.Agent.SkillAuthoringEffective() {
		if logger != nil {
			logger.Info("技能自扩展工具未注册（agent.skill_authoring=false）")
		}
		return
	}
	root, err := skillsRootForAuthoring(cfg)
	if err != nil {
		if logger != nil {
			logger.Warn("技能自扩展工具未注册：技能目录不可解析", zap.Error(err))
		}
		return
	}

	createTool := mcp.Tool{
		Name: builtin.ToolCreateSkill,
		Description: "新建一个技能包（写入 <skills_dir>/<name>/SKILL.md）。用于把本轮验证过的可复用流程沉淀成技能，供后续会话按需加载。" +
			"要求：name 为小写字母/数字/连字符；description 必须写清「何时该用这个技能」（它进 available_skills 索引，直接决定模型会不会加载）；" +
			"body 为可执行正文（步骤、命令、判据、坑），不要写没有操作细节的空话。" +
			"已存在的技能不会被覆盖——修订请用 update_skill_file。新建的技能在下一次运行即出现在技能索引里。",
		ShortDescription: "新建技能包（沉淀可复用流程）",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "技能名（= 目录名），小写字母/数字/连字符，如 web-cache-poisoning",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "一句话说明何时使用（含中英文关键词，便于检索命中）",
				},
				"body": map[string]interface{}{
					"type":        "string",
					"description": "技能正文（Markdown）：可执行流程与判据",
				},
			},
			"required": []string{"name", "description", "body"},
		},
	}
	mcpServer.RegisterTool(createTool, func(_ context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		name := strings.TrimSpace(strArg(args, "name"))
		desc := strings.TrimSpace(strArg(args, "description"))
		body := strArg(args, "body")

		if !validAuthoringSkillName(name) {
			return textResult(fmt.Sprintf("错误: 技能名非法（仅小写字母/数字/连字符，≤%d 字符）: %q", skillAuthoringMaxNameLen, name), true), nil
		}
		if desc == "" || strings.TrimSpace(body) == "" {
			return textResult("错误: description 与 body 均为必填", true), nil
		}
		if len(body) > skillAuthoringMaxFileBytes {
			return textResult(fmt.Sprintf("错误: body 过大（上限 %d 字节）", skillAuthoringMaxFileBytes), true), nil
		}
		dir := filepath.Join(root, name)
		if _, statErr := os.Stat(dir); statErr == nil {
			return textResult("错误: 技能已存在，不会被覆盖——修订请用 update_skill_file，或换一个名字", true), nil
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return textResult("错误: 创建技能目录失败: "+err.Error(), true), nil
		}
		md, err := skillpackage.BuildSkillMD(&skillpackage.SkillManifest{Name: name, Description: desc}, body)
		if err != nil {
			return textResult("错误: 生成 SKILL.md 失败: "+err.Error(), true), nil
		}
		skillFile := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(skillFile, md, 0o644); err != nil {
			return textResult("错误: 写入 SKILL.md 失败: "+err.Error(), true), nil
		}
		if logger != nil {
			logger.Info("agent 新建技能", zap.String("skill", name), zap.String("dir", dir))
		}
		return textResult(fmt.Sprintf("技能已创建：%s\n目录：%s\n下一次运行 skill 工具即可看到它（技能索引每次运行重扫）。", name, dir), false), nil
	})

	updateTool := mcp.Tool{
		Name: builtin.ToolUpdateSkillFile,
		Description: "修订已存在技能包里的文件（默认 SKILL.md；也可写包内脚本/参考文件，file 用相对路径如 scripts/run.sh）。" +
			"只能改已有技能，不会新建；路径不得越出技能目录。改动在下一次运行生效。",
		ShortDescription: "修订技能包内文件",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "技能名（= 目录名）",
				},
				"file": map[string]interface{}{
					"type":        "string",
					"description": "包内相对路径，默认 SKILL.md；如 references/notes.md",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "完整文件内容（整体覆盖）",
				},
			},
			"required": []string{"name", "content"},
		},
	}
	mcpServer.RegisterTool(updateTool, func(_ context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		name := strings.TrimSpace(strArg(args, "name"))
		rel := strArg(args, "file")
		content := strArg(args, "content")

		if !validAuthoringSkillName(name) {
			return textResult(fmt.Sprintf("错误: 技能名非法: %q", name), true), nil
		}
		if len(content) > skillAuthoringMaxFileBytes {
			return textResult(fmt.Sprintf("错误: content 过大（上限 %d 字节）", skillAuthoringMaxFileBytes), true), nil
		}
		dir := filepath.Join(root, name)
		if _, statErr := os.Stat(dir); statErr != nil {
			return textResult("错误: 技能不存在（本工具只改已有技能），新建请用 create_skill", true), nil
		}
		full, err := resolveSkillFilePath(root, name, rel)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		if mkErr := os.MkdirAll(filepath.Dir(full), 0o755); mkErr != nil {
			return textResult("错误: 创建子目录失败: "+mkErr.Error(), true), nil
		}
		if wErr := os.WriteFile(full, []byte(content), 0o644); wErr != nil {
			return textResult("错误: 写入失败: "+wErr.Error(), true), nil
		}
		if logger != nil {
			logger.Info("agent 修订技能文件", zap.String("skill", name), zap.String("file", full))
		}
		return textResult(fmt.Sprintf("已写入：%s（%d 字节）\n下一次运行生效。", full, len(content)), false), nil
	})
}
