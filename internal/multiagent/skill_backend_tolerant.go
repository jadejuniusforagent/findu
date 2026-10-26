package multiagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"finduai/internal/skillpackage"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// tolerantSkillBackend 给 Eino 官方 skill.Backend 加一层容错。
//
// 背景（缺陷来源）：
// Eino v0.9.x 的 filesystemBackend.list() 逐个读取 SKILL.md，任一文件解析失败
// 就整体 return error -> Backend.List 失败 -> skill 工具 Info() 失败 ->
// 该轮所有技能集体不可用。即「新加一个写坏的 SKILL.md 会拖垮现有全部技能」。
// 参考：eino/adk/middlewares/skill/filesystem_backend.go:95-121、skill.go:381-411。
//
// 策略（零行为变更优先）：
//   - 官方实现成功时，原样返回，行为与不装本包装器完全一致；
//   - 官方实现失败时，降级为「逐个读取、跳过坏文件」的容错扫描，并打 Warn 日志。
//
// 这样既修掉脆弱点，又不改变现有 24 个技能的正常路径。
type tolerantSkillBackend struct {
	inner   skill.Backend
	baseDir string
	logger  *zap.Logger
	// cache 非 nil 时，每次成功取到技能就记一笔，供「压缩后技能重注入」使用。
	cache *skillReinjectCache
}

// newTolerantSkillBackend 包装内层 Backend；inner 为空或 baseDir 为空时原样返回（不包装）。
func newTolerantSkillBackend(inner skill.Backend, baseDir string, logger *zap.Logger, cache *skillReinjectCache) skill.Backend {
	if inner == nil || strings.TrimSpace(baseDir) == "" {
		return inner
	}
	return &tolerantSkillBackend{inner: inner, baseDir: baseDir, logger: logger, cache: cache}
}

func (b *tolerantSkillBackend) record(s skill.Skill) {
	if b == nil || b.cache == nil {
		return
	}
	b.cache.record(s)
}

// List 优先走官方实现；失败时降级容错扫描。
func (b *tolerantSkillBackend) List(ctx context.Context) ([]skill.FrontMatter, error) {
	if b.inner != nil {
		fm, err := b.inner.List(ctx)
		if err == nil {
			return fm, nil
		}
		if b.logger != nil {
			b.logger.Warn("eino skill backend List 失败，降级为容错扫描", zap.Error(err))
		}
	}

	skills, skipped := b.scanTolerant(b.baseDir)
	if b.logger != nil && skipped > 0 {
		b.logger.Warn("容错扫描跳过了解析失败的 SKILL.md",
			zap.Int("loaded", len(skills)), zap.Int("skipped", skipped), zap.String("dir", b.baseDir))
	}

	out := make([]skill.FrontMatter, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.FrontMatter)
	}
	return out, nil
}

// Get 优先走官方实现；失败时降级按 name 容错查找。
func (b *tolerantSkillBackend) Get(ctx context.Context, name string) (skill.Skill, error) {
	if b.inner != nil {
		s, err := b.inner.Get(ctx, name)
		if err == nil {
			b.record(s)
			return s, nil
		}
		if b.logger != nil {
			b.logger.Warn("eino skill backend Get 失败，降级为容错扫描",
				zap.String("skill", name), zap.Error(err))
		}
	}

	skills, _ := b.scanTolerant(b.baseDir)
	want := strings.TrimSpace(name)
	for _, s := range skills {
		if strings.EqualFold(strings.TrimSpace(s.Name), want) {
			b.record(s)
			return s, nil
		}
	}
	return skill.Skill{}, fmt.Errorf("skill not found: %s", name)
}

// scanTolerant 只扫一级子目录的 SKILL.md；单个文件失败仅跳过并计数，不影响其余技能。
// 返回 (已加载技能, 跳过计数)。
func (b *tolerantSkillBackend) scanTolerant(dir string) ([]skill.Skill, int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if b.logger != nil {
			b.logger.Warn("容错扫描：读取技能目录失败", zap.String("dir", dir), zap.Error(err))
		}
		return nil, 0
	}

	out := make([]skill.Skill, 0, len(entries))
	skipped := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		raw, readErr := os.ReadFile(filepath.Join(sub, "SKILL.md"))
		if readErr != nil {
			// 该子目录没有 SKILL.md（如 .eino 任务板目录）：正常跳过，不计告警
			continue
		}
		s, parseErr := parseSkillFileTolerant(raw, sub)
		if parseErr != nil {
			skipped++
			if b.logger != nil {
				b.logger.Warn("容错扫描：跳过无法解析的技能", zap.String("dir", sub), zap.Error(parseErr))
			}
			continue
		}
		out = append(out, s)
	}
	return out, skipped
}

// parseSkillFileTolerant 解析 SKILL.md：YAML frontmatter + Markdown 正文。
// 复用内部 skillpackage 的分割逻辑（含 BOM 处理），frontmatter 直接反序列化到
// Eino 的 skill.FrontMatter（字段：name / description / context / agent / model）。
func parseSkillFileTolerant(raw []byte, baseDir string) (skill.Skill, error) {
	fmYAML, body, err := skillpackage.ExtractSkillMDFrontMatterYAML(raw)
	if err != nil {
		return skill.Skill{}, err
	}

	var fm skill.FrontMatter
	if err := yaml.Unmarshal([]byte(fmYAML), &fm); err != nil {
		return skill.Skill{}, fmt.Errorf("frontmatter yaml: %w", err)
	}
	if strings.TrimSpace(fm.Name) == "" {
		// name 为空时 Eino 索引进 <available_skills><name></name> 是空条目，
		// 模型既认不出也调不动，视为无效技能跳过。
		return skill.Skill{}, fmt.Errorf("frontmatter 缺少 name")
	}

	return skill.Skill{
		FrontMatter:   fm,
		Content:       strings.TrimSpace(body),
		BaseDirectory: baseDir,
	}, nil
}
