package multiagent

import (
	"context"
	"path/filepath"
	"testing"

	localbk "github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk/middlewares/skill"
)

// TestAllShippedSkillsLoad 守门：仓库 skills/ 下每个 SKILL.md 都能被 Eino 官方
// 后端解析（frontmatter 合法、name/description 非空）。
//
// 背景：官方 filesystem backend 的 List 在任一 SKILL.md 解析失败时整体报错 ——
// 一个坏文件会让该轮**所有**技能集体不可用（FindUAI 已加 tolerant 包装降级为
// 跳过，但被跳过的技能等于静默失效）。本测试把「全部可加载」钉死，新增技能
// frontmatter 写坏时先在这里红，而不是在生产会话里静默消失。
//
// 说明：不使用本仓的 tolerant 包装，直连官方后端 —— 要的就是「官方解析也全过」。
func TestAllShippedSkillsLoad(t *testing.T) {
	ctx := context.Background()

	abs, err := filepath.Abs(filepath.Join("..", "..", "skills"))
	if err != nil {
		t.Fatalf("解析 skills 目录失败: %v", err)
	}

	loc, err := localbk.NewBackend(ctx, &localbk.Config{})
	if err != nil {
		t.Fatalf("初始化 local backend 失败: %v", err)
	}

	be, err := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
		Backend: loc,
		BaseDir: abs,
	})
	if err != nil {
		t.Fatalf("构造 skill backend 失败: %v", err)
	}

	list, err := be.List(ctx)
	if err != nil {
		t.Fatalf("加载技能失败（存在 frontmatter 非法的 SKILL.md）: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("技能列表为空 —— skills 目录漂移或解析全失败")
	}

	for _, fm := range list {
		if fm.Name == "" {
			t.Errorf("存在 name 为空的技能")
		}
		if fm.Description == "" {
			t.Errorf("技能 %q 的 description 为空（模型无法据此选择）", fm.Name)
		}
	}

	// 下限守卫：技能被误删时先在这里红（当前基线 101，留余量取 90）。
	const minSkills = 90
	if len(list) < minSkills {
		t.Errorf("技能数 %d < 下限 %d —— 技能被批量误删或目录漂移", len(list), minSkills)
	}
	t.Logf("已加载技能 %d 个", len(list))
}
