package db

import (
	"encoding/json"
	"finduai/domain/report"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// 报告域存储迁移到 domain/report 后，db 包只剩别名与转发。
// 这两个用例走 db 包的公开方法，实际打的是 domain/report 的 SQL ——
// 迁移若把某条语句写错，这里就会红。

func TestReportSettingsDefaultsPackageBridge(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // 先于其它 Cleanup 执行：注册得最早，LIFO 最后跑

	prev, err := d.GetReportSettings()
	if err != nil {
		t.Fatalf("读取原设置失败: %v", err)
	}
	t.Cleanup(func() { _ = d.SaveReportSettings(prev) }) // 用例结束还原，别污染共享测试库

	want := &report.Settings{
		CompanyName:   "t-测试公司",
		CompanyNameEn: "t-test-corp",
		LogoPath:      "/tmp/t-logo.png",
		Personnel:     json.RawMessage(`[{"name":"张三","role":"工程师","contact":"13800000000"}]`),
		CVSSVersion:   "4.0",
	}
	if err := d.SaveReportSettings(want); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	got, err := d.GetReportSettings()
	if err != nil {
		t.Fatalf("读回设置失败: %v", err)
	}
	if got.CompanyName != want.CompanyName || got.CompanyNameEn != want.CompanyNameEn ||
		got.LogoPath != want.LogoPath || got.CVSSVersion != want.CVSSVersion {
		t.Fatalf("标量字段不一致: got %+v", got)
	}
	// jsonb 会规范化格式（空格/键序），必须解成值再比，不能按字符串比
	var g, w []map[string]any
	if err := json.Unmarshal(got.Personnel, &g); err != nil {
		t.Fatalf("personnel 不是合法 JSON: %v (%q)", err, string(got.Personnel))
	}
	if err := json.Unmarshal(want.Personnel, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("personnel 内容不一致: got %v want %v", g, w)
	}

	// 只换 logo，不应动其它字段
	if err := d.SetReportLogo("/tmp/t-logo-2.png"); err != nil {
		t.Fatalf("SetReportLogo: %v", err)
	}
	got2, err := d.GetReportSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got2.LogoPath != "/tmp/t-logo-2.png" || got2.CompanyName != want.CompanyName {
		t.Fatalf("SetReportLogo 改了不该改的字段: %+v", got2)
	}
}

func TestReportTemplateCRUDPackageBridge(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // 先于其它 Cleanup 执行：注册得最早，LIFO 最后跑

	name := fmt.Sprintf("t-template-%d", time.Now().UnixNano())
	id, err := d.AddReportTemplate(&report.Template{
		Name:          name,
		CompanyName:   "t-模板公司",
		CompanyNameEn: "t-tpl-corp",
		LogoPath:      "/tmp/tpl.png",
		Personnel:     json.RawMessage(`[{"name":"李四","role":"审核"}]`),
		// CVSSVersion / Locale 故意留空，走 domain 层默认值 3.1 / zh-CN
	})
	if err != nil {
		t.Fatalf("新增模板失败: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteReportTemplate(id) })

	got, err := d.GetReportTemplate(id)
	if err != nil || got == nil {
		t.Fatalf("读取模板失败: got=%v err=%v", got, err)
	}
	if got.Name != name || got.CompanyName != "t-模板公司" {
		t.Fatalf("模板字段不一致: %+v", got)
	}
	if got.CVSSVersion != "3.1" || got.Locale != "zh-CN" {
		t.Fatalf("空值未落默认: cvss=%q locale=%q", got.CVSSVersion, got.Locale)
	}

	got.Locale = "en-US"
	got.CVSSVersion = "4.0"
	if err := d.UpdateReportTemplate(got); err != nil {
		t.Fatalf("更新模板失败: %v", err)
	}
	again, err := d.GetReportTemplate(id)
	if err != nil || again == nil {
		t.Fatalf("更新后读取失败: %v", err)
	}
	if again.Locale != "en-US" || again.CVSSVersion != "4.0" {
		t.Fatalf("更新未生效: %+v", again)
	}

	list, err := d.ListReportTemplates()
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	found := false
	for _, x := range list {
		if x.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("列表里找不到刚创建的模板 id=%d（共 %d 条）", id, len(list))
	}

	if err := d.DeleteReportTemplate(id); err != nil {
		t.Fatalf("删除模板失败: %v", err)
	}
	if after, err := d.GetReportTemplate(id); err != nil || after != nil {
		t.Fatalf("删除后仍能读到: %v err=%v", after, err)
	}
}
