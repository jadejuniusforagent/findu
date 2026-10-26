package db

// 任务配置组（分类 / 模板 / 任务级规则 / 约束）迁到 domain/task 与
// domain/exploration 后，db 包只剩别名与转发。这些用例走 db 包的公开方法，
// 实际打到 domain 层的新 SQL —— 搬迁若把语句或语义写错，这里会红。
//
// 重点守两件事：
//  1. 删除语义：删分类只把任务/模板退回未分类（ON DELETE SET NULL），不删它们。
//  2. 规则快照：模板 patch「只改名字」时绝不能把 intercept_rules 冲成 []。

import (
	"errors"
	dtask "finduai/domain/task"
	"fmt"
	"testing"
	"time"
)

func uniqueName(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }

// 删分类 → 任务还在、只是 category_id 变 NULL；模板同理。
// 原始实现只对任务做过断言，模板这一半是空的，这里补上。
func TestTaskCategoryDeleteNullsTaskAndTemplateRefs(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // 先于其它 Cleanup 执行：注册得最早，LIFO 最后跑

	category, err := d.CreateTaskCategory(uniqueName("t-cat"))
	if err != nil {
		t.Fatalf("建分类失败: %v", err)
	}

	task, err := d.CreateTaskWithOptions("t-categorized", "goal", TaskCreateOptions{CategoryID: &category.ID})
	if err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	tmpl, err := d.CreateTaskTemplate(dtask.TemplateInput{
		Name:        uniqueName("t-tmpl"),
		Description: "desc",
		Goal:        "goal",
		CategoryID:  &category.ID,
	})
	if err != nil {
		t.Fatalf("建模板失败: %v", err)
	}
	t.Cleanup(func() { _, _ = d.DeleteTaskTemplate(tmpl.ID) })

	if tmpl.CategoryID == nil || *tmpl.CategoryID != category.ID {
		t.Fatalf("模板没落 category_id: %+v", tmpl)
	}

	deleted, err := d.DeleteTaskCategory(category.ID)
	if err != nil || !deleted {
		t.Fatalf("删分类=%v err=%v", deleted, err)
	}

	// 任务：行还在，分类被外键置空
	loadedTask, err := d.GetTask(task.ID)
	if err != nil || loadedTask == nil {
		t.Fatalf("删分类把任务一起删了: task=%+v err=%v", loadedTask, err)
	}
	if loadedTask.CategoryID != nil || loadedTask.CategoryName != "" {
		t.Fatalf("任务分类未被置空: %+v", loadedTask)
	}

	// 模板：行还在，分类被外键置空（这一条原先没有覆盖）
	loadedTmpl, err := d.GetTaskTemplate(tmpl.ID)
	if err != nil || loadedTmpl == nil {
		t.Fatalf("删分类把模板一起删了: tmpl=%+v err=%v", loadedTmpl, err)
	}
	if loadedTmpl.CategoryID != nil {
		t.Fatalf("模板分类未被置空: %+v", loadedTmpl)
	}
}

func TestTaskTemplateRulesRoundTripAndPartialPatch(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // 先于其它 Cleanup 执行：注册得最早，LIFO 最后跑

	rules := []dtask.InterceptRuleInput{
		{Enabled: true, Action: "block", Kind: "exact_domain", Pattern: "blocked.example.com", Note: "禁止测试"},
		{Enabled: true, Action: "allow", Kind: "cidr", Pattern: "10.0.0.0/8", Note: "内网白名单"},
	}
	tmpl, err := d.CreateTaskTemplate(dtask.TemplateInput{
		Name:           uniqueName("t-rules"),
		Description:    "带规则的模板",
		Goal:           "验证规则快照",
		InterceptRules: rules,
	})
	if err != nil {
		t.Fatalf("建模板失败: %v", err)
	}
	t.Cleanup(func() { _, _ = d.DeleteTaskTemplate(tmpl.ID) })

	got, err := d.GetTaskTemplate(tmpl.ID)
	if err != nil || got == nil {
		t.Fatalf("读模板失败: %+v err=%v", got, err)
	}
	if len(got.InterceptRules) != 2 {
		t.Fatalf("规则条数=%d, want 2", len(got.InterceptRules))
	}
	if got.InterceptRules[0].Action != "block" || got.InterceptRules[0].Pattern != "blocked.example.com" {
		t.Fatalf("规则 0 内容错: %+v", got.InterceptRules[0])
	}
	if got.InterceptRules[1].Action != "allow" || got.InterceptRules[1].Kind != "cidr" {
		t.Fatalf("规则 1 内容错: %+v", got.InterceptRules[1])
	}

	// 只改名：规则必须原样保留（旧实现在这里容易把 nil 规则写成 []）
	onlyName := got.Name + "-renamed"
	patched, err := d.PatchTaskTemplate(tmpl.ID, dtask.TemplatePatch{Name: &onlyName})
	if err != nil {
		t.Fatalf("只改名失败: %v", err)
	}
	if len(patched.InterceptRules) != 2 {
		t.Fatalf("改名把规则冲掉了: %+v", patched.InterceptRules)
	}

	// 只改规则：名字/描述/目标不动
	one := []dtask.InterceptRuleInput{{Enabled: false, Action: "block", Kind: "exact_ip", Pattern: "192.0.2.7"}}
	patched, err = d.PatchTaskTemplate(tmpl.ID, dtask.TemplatePatch{InterceptRules: one, SetInterceptRules: true})
	if err != nil {
		t.Fatalf("只改规则失败: %v", err)
	}
	if patched.Name != onlyName || patched.Description != "带规则的模板" || patched.Goal != "验证规则快照" {
		t.Fatalf("改规则动了别的字段: %+v", patched)
	}
	if len(patched.InterceptRules) != 1 || patched.InterceptRules[0].Pattern != "192.0.2.7" || patched.InterceptRules[0].Enabled {
		t.Fatalf("规则未替换: %+v", patched.InterceptRules)
	}

	if _, err := d.PatchTaskTemplate(tmpl.ID, dtask.TemplatePatch{}); !errors.Is(err, dtask.ErrTemplateInvalid) {
		t.Fatalf("空 patch 应报 invalid, got %v", err)
	}
}

func TestTaskInterceptRulesCRUDSplitAndCascade(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // 先于其它 Cleanup 执行：注册得最早，LIFO 最后跑

	task, err := d.CreateTaskWithOptions("t-intercepts", "goal", TaskCreateOptions{
		InterceptRules: []dtask.InterceptRuleInput{
			{Enabled: true, Action: "block", Kind: "exact_domain", Pattern: "blocked.example.com"},
		},
	})
	if err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	rules, err := d.Assets().ListTaskInterceptRules(task.ID)
	if err != nil {
		t.Fatalf("读规则失败: %v", err)
	}
	if len(rules) != 1 || rules[0].Pattern != "blocked.example.com" || rules[0].Action != "block" {
		t.Fatalf("创建任务时带的规则没落库: %+v", rules)
	}

	allow, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "cidr", "10.1.0.0/16", "白名单", true)
	if err != nil {
		t.Fatalf("建 allow 规则失败: %v", err)
	}
	block, allowRules, err := d.Assets().TaskInterceptRulesSplit(task.ID)
	if err != nil {
		t.Fatalf("拆分失败: %v", err)
	}
	if len(block) != 1 || len(allowRules) != 1 || allowRules[0].ID != allow.ID {
		t.Fatalf("拆分结果错: block=%+v allow=%+v", block, allowRules)
	}

	if err := d.Assets().ToggleTaskInterceptRule(task.ID, allow.ID, false); err != nil {
		t.Fatalf("toggle 失败: %v", err)
	}
	rules, _ = d.Assets().ListTaskInterceptRules(task.ID)
	for _, r := range rules {
		if r.ID == allow.ID && r.Enabled {
			t.Fatalf("toggle 未生效: %+v", r)
		}
	}

	// 更新时 action 归一：非 allow 一律 block
	updated, err := d.Assets().UpdateTaskInterceptRule(task.ID, allow.ID, "ALLOW", "exact_ip", "192.0.2.9", "改过", true)
	if err != nil {
		t.Fatalf("更新规则失败: %v", err)
	}
	if updated.Action != "block" || updated.Pattern != "192.0.2.9" || !updated.Enabled {
		t.Fatalf("更新结果错: %+v", updated)
	}

	deleted, err := d.Assets().DeleteTaskInterceptRule(task.ID, allow.ID)
	if err != nil || !deleted {
		t.Fatalf("删规则=%v err=%v", deleted, err)
	}
	if deleted, err = d.Assets().DeleteTaskInterceptRule(task.ID, allow.ID); err != nil || deleted {
		t.Fatalf("重复删应返回 false: %v %v", deleted, err)
	}

	// 删任务 → 规则随外键 CASCADE 一起消失
	if err := d.DeleteTask(task.ID); err != nil {
		t.Fatalf("删任务失败: %v", err)
	}
	var left int
	if err := d.QueryRow(`SELECT count(*) FROM task_intercept_rules WHERE task_id=$1`, task.ID).Scan(&left); err != nil {
		t.Fatalf("查残留规则失败: %v", err)
	}
	if left != 0 {
		t.Fatalf("删任务后残留 %d 条规则（CASCADE 未生效）", left)
	}
}

func TestConstraintsOrderingAndScoping(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // 先于其它 Cleanup 执行：注册得最早，LIFO 最后跑

	expID, err := d.CreateExploration("t-constraints", "goal")
	if err != nil {
		t.Fatalf("建探索失败: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) })
	sc := d.Exploration(expID)

	denyID, err := sc.AddConstraint("deny", "禁止 rm -rf", "")
	if err != nil {
		t.Fatalf("加 deny 失败: %v", err)
	}
	if _, err := sc.AddConstraint("allow", "允许只读扫描", "human"); err != nil {
		t.Fatalf("加 allow 失败: %v", err)
	}
	if _, err := sc.AddConstraint("maybe", "非法 kind", ""); err == nil {
		t.Fatal("非法 kind 应当报错")
	}

	list, err := sc.ListConstraints()
	if err != nil {
		t.Fatalf("读约束失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("约束条数=%d, want 2", len(list))
	}
	// allow 在前、deny 在后
	if list[0].Kind != "allow" || list[1].Kind != "deny" {
		t.Fatalf("顺序错: %+v", list)
	}
	// origin 未传时默认 system；传了 human 就是 human
	if list[0].Origin != "human" || list[1].Origin != "system" {
		t.Fatalf("origin 错: %+v", list)
	}

	// 另一条探索看不到这些约束（按 exploration_id 隔离）
	otherExp, err := d.CreateExploration("t-constraints-other", "goal")
	if err != nil {
		t.Fatalf("建第二个探索失败: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, otherExp) })
	if got, err := d.Exploration(otherExp).ListConstraints(); err != nil || len(got) != 0 {
		t.Fatalf("约束串了探索: %+v err=%v", got, err)
	}

	if err := sc.UpdateConstraint(denyID, "allow", "改为允许"); err != nil {
		t.Fatalf("改约束失败: %v", err)
	}
	if err := sc.UpdateConstraint(denyID, "bogus", "x"); err == nil {
		t.Fatal("改约束的非法 kind 应当报错")
	}
	if err := d.Exploration(otherExp).DeleteConstraint(denyID); err == nil {
		t.Fatal("跨探索删除应当失败")
	}
	if err := sc.DeleteConstraint(denyID); err != nil {
		t.Fatalf("删约束失败: %v", err)
	}
	if err := sc.DeleteConstraint(denyID); err == nil {
		t.Fatal("重复删应当报「约束不存在」")
	}
}
