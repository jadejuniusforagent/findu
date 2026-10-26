package smali

// smali_standard_test — StandardClass 输出格式验证(不依赖 smali.jar)。
// 断言标准 smali 语法约束: .class 带 access flags、.super 存在、
// 无 i0000 地址前缀、无 kind@ 注释、无 (ins/outs) 扩展。

import (
	"path/filepath"
	"strings"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

func TestStandardClassFormat(t *testing.T) {
	d := fixtureDex(t)
	cls := d.GetClassByIdx(0)
	lines := StandardClass(d, cls)

	// .class 必须有 access flags(public/private/...)
	clsLine := ""
	for _, l := range lines {
		if strings.HasPrefix(l, ".class ") {
			clsLine = l
			break
		}
	}
	if clsLine == "" {
		t.Fatal("missing .class line")
	}
	// .class <flags> <name>: flags 部分不能为空
	parts := strings.SplitN(strings.TrimPrefix(clsLine, ".class "), " ", 2)
	if len(parts) < 2 || parts[0] == "" {
		t.Errorf(".class missing access flags: %q", clsLine)
	}

	// .super 必须存在
	hasSuper := false
	for _, l := range lines {
		if strings.HasPrefix(l, ".super ") {
			hasSuper = true
			break
		}
	}
	if !hasSuper {
		t.Error("missing .super")
	}

	// 指令行不应有 i0000 前缀(标准 smali 无地址前缀)
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "i") && len(trimmed) > 5 && trimmed[5] == ':' {
			t.Errorf("standard smali should not have address prefix: %q", trimmed)
		}
	}

	// 指令行不应有 kind@ 注释(标准 smali 用完整引用)
	for _, l := range lines {
		if strings.Contains(l, "string@ ") || strings.Contains(l, "type@ ") ||
			strings.Contains(l, "field@ ") || strings.Contains(l, "meth@ ") {
			t.Errorf("standard smali should not have kind@ annotation: %q", strings.TrimSpace(l))
		}
	}

	// .registers 行不应有 (ins/outs) 扩展
	for _, l := range lines {
		if strings.Contains(l, "(ins ") || strings.Contains(l, "outs ") {
			t.Errorf("standard smali .registers should not have (ins/outs): %q", l)
		}
	}

	// 方法块 .method ... .end method 完整
	methodCount := 0
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), ".method ") {
			methodCount++
		}
	}
	if methodCount == 0 {
		t.Error("no .method found in standard output")
	}
	t.Logf("standard output: %d lines, %d methods", len(lines), methodCount)
}

func TestDexClassSuper(t *testing.T) {
	d := fixtureDex(t)
	cls := d.GetClassByIdx(0)
	super := cls.Super()
	if super == "" {
		t.Error("Super() returned empty")
	}
	// 任何合法描述符应以 L 开头, ; 结尾
	if !strings.HasPrefix(super, "L") || !strings.HasSuffix(super, ";") {
		t.Errorf("Super() = %q, want L...; format", super)
	}
	t.Logf("Super() = %s", super)
}

func TestDexClassAccessFlags(t *testing.T) {
	d := fixtureDex(t)
	for i := 0; i < int(d.NumClasses()); i++ {
		cls := d.GetClassByIdx(uint32(i))
		flags := cls.ClassAccessFlags()
		// 至少应该有一些标志位(每个类至少有 ACC_PUBLIC 或 ACC_FINAL 等)
		_ = flags // 不强制,但确保不 panic
	}
}

func TestStandardClassAllFixtures(t *testing.T) {
	matches, _ := filepath.Glob(filepath.Join("..", "findrefs", "testdata", "*.apk"))
	if len(matches) == 0 {
		t.Skip("no fixtures")
	}
	for _, apkPath := range matches {
		r, err := zipprobe.Open(apkPath)
		if err != nil {
			t.Fatal(err)
		}
		entries, _ := zipprobe.ParseCDDexEntries(r)
		for _, e := range entries {
			data, err := zipprobe.InflateDex(r, e)
			if err != nil {
				continue
			}
			for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
				d, err := dex.Parse(ld.Buf, ld.Name)
				if err != nil {
					continue
				}
				for i := 0; i < int(d.NumClasses()); i++ {
					cls := d.GetClassByIdx(uint32(i))
					lines := StandardClass(d, cls)
					for _, l := range lines {
						if strings.Contains(l, "(unknown format)") {
							t.Errorf("%s %s: unknown format in standard output", apkPath, cls.Fullname())
						}
					}
				}
			}
		}
		r.Close()
	}
}
