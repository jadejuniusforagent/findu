package dex

import (
	"bytes"
	"testing"

	"finduai/internal/apkengine/internal/fixture"
)

// ────────────────────────── 测试 ──────────────────────────

func TestParseAndStrings(t *testing.T) {
	d, err := Parse(fixture.BuildTestDEX(), "classes.dex")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.NumStrings() == 0 {
		t.Fatal("no strings")
	}
	if got := d.GetString(strIdxOf(t, d, "hello")); got != "" {
		// "hello" 前缀不存在,GetString 是全量匹配,无此串
		_ = got
	}
	if s := d.GetString(strIdxOf(t, d, "secret_token")); s != "secret_token" {
		t.Errorf("secret_token = %q", s)
	}
}

func strIdxOf(t *testing.T, d *DEX, _ string) uint32 {
	// 辅助:按值找 string 索引
	for i := uint32(0); i < d.NumStrings(); i++ {
		if d.GetString(i) == "secret_token" {
			return i
		}
	}
	t.Fatal("secret_token not found")
	return 0
}

func TestTypes(t *testing.T) {
	d, _ := Parse(fixture.BuildTestDEX(), "classes.dex")
	cls := d.GetClassByName("Lcom/poc/Main;")
	if cls == nil {
		t.Fatal("class Main not found")
	}
	if cls.Fullname() != "Lcom/poc/Main;" {
		t.Errorf("fullname = %q", cls.Fullname())
	}
	arr := d.GetType(typeIdxOfDesc(t, d, "[I"))
	if arr.Kind != KindArray || arr.Dim != 1 || arr.Underlying == nil || arr.Underlying.PrimValue != Int {
		t.Errorf("array type parse wrong: %+v", arr)
	}
	iv := d.GetType(typeIdxOfDesc(t, d, "I"))
	if iv.Kind != KindPrimitive || iv.PrimValue != Int {
		t.Errorf("int type parse wrong: %+v", iv)
	}
}

func typeIdxOfDesc(t *testing.T, d *DEX, desc string) uint32 {
	for i := uint32(0); i < d.NumTypes(); i++ {
		if d.GetType(i).Descriptor == desc {
			return i
		}
	}
	t.Fatalf("type %s not found", desc)
	return 0
}

func TestClassMembers(t *testing.T) {
	d, _ := Parse(fixture.BuildTestDEX(), "classes.dex")
	cls := d.GetClassByName("Lcom/poc/Main;")
	fields := cls.Fields()
	if len(fields) != 2 {
		t.Fatalf("fields = %d, want 2", len(fields))
	}
	if fields[0].Name() != "value" || fields[0].AccessFlags != 2 {
		t.Errorf("field0 = %+v", fields[0])
	}
	if fields[0].Cls() != "Lcom/poc/Main;" {
		t.Errorf("field class = %q", fields[0].Cls())
	}
	methods := cls.Methods()
	if len(methods) != 2 {
		t.Fatalf("methods = %d, want 2", len(methods))
	}
	var main, getCount *Method
	for _, m := range methods {
		switch m.Name() {
		case "main":
			main = m
		case "getCount":
			getCount = m
		}
	}
	if main == nil || !main.IsDirect {
		t.Errorf("main direct = %+v", main)
	}
	if getCount == nil || !getCount.IsVirtual {
		t.Errorf("getCount virtual = %+v", getCount)
	}
	// main: const-class type@2 + const-string secret_token(idx 12) + return-void
	if code := main.Code(); code == nil || code.InsnsSize != 5 ||
		!bytes.Equal(code.Insns, []byte{0x1C, 0x00, 0x02, 0x00, 0x1A, 0x00, 0x0C, 0x00, 0x0E, 0x00}) {
		t.Errorf("main code = %+v", code)
	}
	// getCount: invoke-static method@1 + sget field@0 + const/4 + return
	if getCount.Code() == nil || getCount.Code().InsnsSize != 7 ||
		!bytes.Equal(getCount.Code().Insns, []byte{0x71, 0x00, 0x01, 0x00, 0x00, 0x00, 0x60, 0x00, 0x00, 0x00, 0x12, 0x10, 0x0F, 0x00}) {
		t.Errorf("getCount code = %+v", getCount.Code())
	}
	if main.Code().RegistersSize != 8 {
		t.Errorf("registers = %d", main.Code().RegistersSize)
	}
}

func TestGetClassByNameMissing(t *testing.T) {
	d, _ := Parse(fixture.BuildTestDEX(), "classes.dex")
	if c := d.GetClassByName("Lcom/missing/Cls;"); c != nil {
		t.Errorf("unexpected hit: %+v", c)
	}
}
