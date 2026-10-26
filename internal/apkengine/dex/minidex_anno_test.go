package dex

// minidex_anno_test — encoded_value 重写的字节级单元测试(包内:直接构造
// 未导出的 minidexBuilder)。端到端注解结构一致性由 dex_test 包覆盖。

import "testing"

func newTestBuilder() *minidexBuilder {
	return &minidexBuilder{
		d:              &DEX{},
		strs:           map[uint32]bool{},
		types:          map[uint32]bool{},
		protos:         map[uint32]bool{},
		fields:         map[uint32]mdField{},
		meths:          map[uint32]mdMethod{},
		codeByOff:      map[uint32]*mdCode{},
		annoDirMap:     map[int]uint32{},
		annoSetMap:     map[int]uint32{},
		annoItemMap:    map[int]uint32{},
		annoRefListMap: map[int]uint32{},
		annoStats:      map[uint16]*annoStat{},
		strMap:         map[uint32]uint32{7: 2, 8: 3},
		typeMap:        map[uint32]uint32{5: 1},
		protoMap:       map[uint32]uint32{9: 4},
		fieldMap:       map[uint32]uint32{11: 6},
		methMap:        map[uint32]uint32{13: 12},
	}
}

// TestRewriteEncodedValueIndexes 索引型值的载荷重映射 + 宽度收缩。
func TestRewriteEncodedValueIndexes(t *testing.T) {
	e := newTestBuilder()
	// string 值:vt=0x17,arg=1(2 字节载荷),旧 idx=7 → 新 idx=2
	src := []byte{0x17 | 1<<5, 0x07, 0x00}
	w := &minidexWriter{}
	pos, err := e.rewriteEncodedValue(w, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pos != len(src) {
		t.Fatalf("pos = %d, want %d", pos, len(src))
	}
	got := w.buf
	if got[0] != 0x17 { // 宽度收缩为 1 字节:arg=0
		t.Errorf("header = %#x, want 0x17 (arg=0)", got[0])
	}
	if got[1] != 0x02 {
		t.Errorf("payload = %d, want 2 (remapped string idx)", got[1])
	}
}

// TestRewriteEncodedValueScalar 纯数值(整型)载荷原样拷贝。
func TestRewriteEncodedValueScalar(t *testing.T) {
	e := newTestBuilder()
	// int:vt=0x04,arg=3(4 字节载荷 42)
	src := []byte{0x04 | 3<<5, 42, 0, 0, 0}
	w := &minidexWriter{}
	if _, err := e.rewriteEncodedValue(w, src, 0); err != nil {
		t.Fatal(err)
	}
	if !bytesEqualT(w.buf, src) {
		t.Errorf("scalar payload should copy verbatim: % x", w.buf)
	}
}

// TestRewriteEncodedValueArray 数组逐元素递归重写。
func TestRewriteEncodedValueArray(t *testing.T) {
	e := newTestBuilder()
	// array:vt=0x1c,arg=2;元素均为 arg=1(2 字节载荷)的 string
	// 旧 idx 7→2、8→3:新索引宽度收缩为 1 字节
	src := []byte{0x1c | 2<<5, 0x17 | 1<<5, 0x07, 0x00, 0x17 | 1<<5, 0x08, 0x00}
	w := &minidexWriter{}
	if _, err := e.rewriteEncodedValue(w, src, 0); err != nil {
		t.Fatal(err)
	}
	if w.buf[0] != 0x1c|2<<5 {
		t.Errorf("array header = %#x", w.buf[0])
	}
	// 元素宽度随新索引收缩:旧 2 字节载荷 → 新 1 字节
	want := []byte{0x1c | 2<<5, 0x17, 0x02, 0x17, 0x03}
	if !bytesEqualT(w.buf, want) {
		t.Errorf("array = % x, want % x", w.buf, want)
	}
}

// TestRewriteEncodedValueMethodHandleRejected method_handle 值触发降级错误。
func TestRewriteEncodedValueMethodHandleRejected(t *testing.T) {
	e := newTestBuilder()
	src := []byte{0x16, 0x01, 0x00}
	w := &minidexWriter{}
	if _, err := e.rewriteEncodedValue(w, src, 0); err == nil {
		t.Fatal("expected error for method_handle value")
	}
}

// TestRewriteEncodedValueBooleanNull boolean/null 零载荷形态保持。
func TestRewriteEncodedValueBooleanNull(t *testing.T) {
	e := newTestBuilder()
	// boolean true:value_arg=1 → 0x1f|1<<5 = 0x3f
	src := []byte{0x3f}
	w := &minidexWriter{}
	if _, err := e.rewriteEncodedValue(w, src, 0); err != nil {
		t.Fatal(err)
	}
	if !bytesEqualT(w.buf, src) {
		t.Errorf("boolean should copy verbatim")
	}
}

func bytesEqualT(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
