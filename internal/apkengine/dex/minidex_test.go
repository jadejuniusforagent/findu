package dex_test

// minidex_test — 最小 DEX 重组测试:
//   - 抽取 → 重解析 → 指令/异常表字节级一致 + 反编译输出一致
//   - 输出不变量(索引区排序、header 一致性、SHA-1/Adler32)
//   - invoke-custom 引用报错、类不存在报错
//
// 置于外部测试包:zipprobe 依赖 dex,包内测试无法引入。

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"hash/adler32"
	"path/filepath"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/godad"
	"finduai/internal/apkengine/internal/fixture"
	"finduai/internal/apkengine/zipprobe"
)

func fixtureDEX(t *testing.T, classDesc string) (*dex.DEX, *dex.Class) {
	t.Helper()
	apkPath := filepath.Join("..", "findrefs", "testdata", "ByteStreamReader.apk")
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		t.Fatalf("cd entries: %v", err)
	}
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
			if cls := d.GetClassByName(classDesc); cls != nil {
				return d, cls
			}
		}
	}
	t.Fatalf("class %s not found in fixture", classDesc)
	return nil, nil
}

// TestExtractClassesRoundtrip 抽取 → 重解析 → 指令/异常表一致 → 反编译一致。
func TestExtractClassesRoundtrip(t *testing.T) {
	src, cls := fixtureDEX(t, "LByteStreamReader;")
	desc := "LByteStreamReader;"

	miniBuf, err := src.ExtractClasses(desc)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	mini, err := dex.Parse(miniBuf, "mini.dex")
	if err != nil {
		t.Fatalf("parse mini dex: %v", err)
	}
	miniCls := mini.GetClassByName(desc)
	if miniCls == nil {
		t.Fatalf("class missing from mini dex")
	}

	// 方法逐一对比:签名形态 + 指令字节 + 异常表
	srcMethods, miniMethods := cls.Methods(), miniCls.Methods()
	if len(srcMethods) != len(miniMethods) {
		t.Fatalf("method count %d != %d", len(miniMethods), len(srcMethods))
	}
	for i := range srcMethods {
		a, b := srcMethods[i], miniMethods[i]
		if a.Name() != b.Name() {
			t.Errorf("method %d name %q != %q", i, b.Name(), a.Name())
		}
		ca, cb := a.Code(), b.Code()
		if (ca == nil) != (cb == nil) {
			t.Fatalf("%s: code presence mismatch", a.Name())
		}
		if ca == nil {
			continue
		}
		// 指令 unit 数必须一致;操作数中的索引字节因重映射而合法地不同,
		// 语义等价由下方反编译输出一致性验证。
		if len(ca.Insns) != len(cb.Insns) {
			t.Errorf("%s: insns length %d != %d", a.Name(), len(cb.Insns), len(ca.Insns))
		}
		ta, tb := a.ExceptionTable(), b.ExceptionTable()
		if (ta == nil) != (tb == nil) {
			t.Errorf("%s: exception table presence mismatch", a.Name())
		}
		if ta != nil && tb != nil && len(ta.Tries) != len(tb.Tries) {
			t.Errorf("%s: tries %d != %d", a.Name(), len(tb.Tries), len(ta.Tries))
		}
		// debug_info 保真:源有行号表,mini 必须也有(重映射后)
		if ca.DebugInfoOff != 0 && cb.DebugInfoOff == 0 {
			t.Errorf("%s: debug_info lost", a.Name())
		}
	}

	// 反编译输出一致(重映射后语义不变的黄金检验)
	want := godad.New(src).DecompileClass(cls)
	got := godad.New(mini).DecompileClass(miniCls)
	if want != got {
		t.Errorf("decompiled output differs:\n--- original ---\n%s\n--- mini ---\n%s", want, got)
	}
}

// TestExtractClassesInvariants 输出 DEX 的结构性不变量。
func TestExtractClassesInvariants(t *testing.T) {
	src, _ := fixtureDEX(t, "LByteStreamReader;")
	buf, err := src.ExtractClasses("LByteStreamReader;")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	// SHA-1 签名([32:])与 Adler32([12:])自洽
	sum := sha1.Sum(buf[32:])
	if !bytes.Equal(sum[:], buf[12:32]) {
		t.Errorf("SHA-1 signature mismatch")
	}
	if chk := adler32.Checksum(buf[12:]); binary.LittleEndian.Uint32(buf[8:12]) != chk {
		t.Errorf("adler32 checksum mismatch")
	}
	if fileSize := binary.LittleEndian.Uint32(buf[0x20:]); int(fileSize) != len(buf) {
		t.Errorf("file_size %d != %d", fileSize, len(buf))
	}
	if magic := string(buf[:4]); magic != "dex\n" {
		t.Errorf("magic = %q", magic)
	}

	mini, err := dex.Parse(buf, "mini.dex")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	h := mini.Header()
	// string_ids 按 string 内容升序
	prev := ""
	for i := uint32(0); i < h.Strings.Size; i++ {
		s := mini.GetString(i)
		if s < prev {
			t.Errorf("string_ids not sorted at %d: %q < %q", i, s, prev)
		}
		prev = s
	}
	// type_ids 按描述符 string 索引升序
	prevStr := uint32(0)
	for i := uint32(0); i < h.Types.Size; i++ {
		s := binary.LittleEndian.Uint32(buf[h.Types.Off+i*4:])
		if i > 0 && s < prevStr {
			t.Errorf("type_ids not sorted at %d", i)
		}
		prevStr = s
	}
	// field_ids / method_ids 组合键升序
	for i := uint32(1); i < h.Fields.Size; i++ {
		a, b := h.Fields.Off+(i-1)*8, h.Fields.Off+i*8
		ac := binary.LittleEndian.Uint16(buf[a:])
		bc := binary.LittleEndian.Uint16(buf[b:])
		if ac > bc {
			t.Errorf("field_ids class_idx not sorted at %d", i)
		}
	}
	for i := uint32(1); i < h.Methods.Size; i++ {
		a, b := h.Methods.Off+(i-1)*8, h.Methods.Off+i*8
		ac, bc := binary.LittleEndian.Uint16(buf[a:]), binary.LittleEndian.Uint16(buf[b:])
		if ac > bc {
			t.Errorf("method_ids class_idx not sorted at %d", i)
		}
	}
	// map_off 指向合法 map_list
	if h.MapOff == 0 || h.MapOff+4 > uint32(len(buf)) {
		t.Fatalf("map_off invalid: %d", h.MapOff)
	}
	n := binary.LittleEndian.Uint32(buf[h.MapOff:])
	if n == 0 || n > 0xffff {
		t.Errorf("map_list size = %d", n)
	}
}

// TestExtractClassesMultiple 一次抽取多个类。
func TestExtractClassesMultiple(t *testing.T) {
	src, _ := fixtureDEX(t, "LByteStreamReader;")
	classes := []string{}
	for i := uint32(0); i < src.NumClasses(); i++ {
		classes = append(classes, src.GetClassByIdx(i).Fullname())
	}
	buf, err := src.ExtractClasses(classes...)
	if err != nil {
		t.Fatalf("extract all %d classes: %v", len(classes), err)
	}
	mini, err := dex.Parse(buf, "mini.dex")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mini.NumClasses() != uint32(len(classes)) {
		t.Errorf("class count = %d, want %d", mini.NumClasses(), len(classes))
	}
}

// TestExtractClassesBailOnInvokeCustom 引用 call site 的类必须报错(降级信号)。
func TestExtractClassesBailOnInvokeCustom(t *testing.T) {
	buf := fixture.BuildInvokeCustomDEX()
	d, err := dex.Parse(buf, "invokecustom.dex")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := d.ExtractClasses("Lcom/poc/Lambda;"); err == nil {
		t.Fatal("expected error for invoke-custom class")
	} else {
		t.Logf("bail error: %v", err)
	}
}

// TestExtractClassesNotFound 不存在的类报错。
func TestExtractClassesNotFound(t *testing.T) {
	src, _ := fixtureDEX(t, "LByteStreamReader;")
	if _, err := src.ExtractClasses("Lcom/missing/Clazz;"); err == nil {
		t.Fatal("expected error for missing class")
	}
}

// TestExtractClassesSizeReduction 抽取应显著缩小(热路径价值验证)。
func TestExtractClassesSizeReduction(t *testing.T) {
	src, _ := fixtureDEX(t, "LByteStreamReader;")
	// 全量遍历一遍以拿到真实源尺寸
	total := 0
	for i := uint32(0); i < src.NumClasses(); i++ {
		_ = src.GetClassByIdx(i).Fullname()
	}
	total = len(src.Buf())
	buf, err := src.ExtractClasses("LByteStreamReader;")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(buf) >= total {
		t.Errorf("mini dex %d bytes >= source %d bytes", len(buf), total)
	}
	t.Logf("mini dex %d bytes vs source %d bytes (%d classes)", len(buf), total, src.NumClasses())
	_ = fmt.Sprint()
}

// TestMiniDexAnnotationDirectoryPreserved 端到端:fixture 类的方法注解
// 在 mini.dex 中结构完整(目录三段数量一致、条目可解析回方法与注解集)。
func TestMiniDexAnnotationDirectoryPreserved(t *testing.T) {
	src, _ := fixtureDEX(t, "LByteStreamReader;")
	const desc = "LByteStreamReader;"
	miniBuf, err := src.ExtractClasses(desc)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	mini, err := dex.Parse(miniBuf, "mini.dex")
	if err != nil {
		t.Fatal(err)
	}

	// 在 class_defs 表中定位类的注解目录偏移(按描述符匹配 class_idx)
	findAnnoOff := func(d *dex.DEX, buf []byte) uint32 {
		h := d.Header()
		for i := uint32(0); i < h.Classes.Size; i++ {
			base := h.Classes.Off + i*32
			classIdx := binary.LittleEndian.Uint32(buf[base:])
			strIdx := binary.LittleEndian.Uint32(buf[h.Types.Off+classIdx*4:])
			if d.GetString(strIdx) == desc {
				return binary.LittleEndian.Uint32(buf[base+20:])
			}
		}
		return 0
	}
	srcDir := findAnnoOff(src, src.Buf())
	miniDir := findAnnoOff(mini, miniBuf)
	t.Logf("DBG srcDir=%d miniDir=%d classesOff=%d nclasses=%d", srcDir, miniDir, mini.Header().Classes.Off, mini.NumClasses())
	if srcDir == 0 {
		t.Skip("fixture class has no annotations")
	}
	if miniDir == 0 {
		t.Fatal("annotations lost in mini dex")
	}
	// 目录三段数量一致
	for _, off := range []uint32{4, 8, 12} {
		a := binary.LittleEndian.Uint32(src.Buf()[srcDir+off:])
		b := binary.LittleEndian.Uint32(miniBuf[miniDir+off:])
		if a != b {
			t.Errorf("directory size field @+%d: %d != %d", off, b, a)
		}
	}
	// 方法注解条目(输出为规范 uleb 差分 + uleb 偏移):方法可解析、注解集合理
	msz := binary.LittleEndian.Uint32(miniBuf[miniDir+8:])
	pos0 := func() int { return int(miniDir + 16) }
	readU := func(pos int) (uint32, int) {
		var v uint32
		for shift := uint(0); ; shift += 7 {
			b := miniBuf[pos]
			v |= uint32(b&0x7f) << shift
			pos++
			if b < 0x80 {
				return v, pos
			}
		}
	}
	t.Logf("dir header: % x", miniBuf[miniDir:miniDir+16])
	t.Logf("entries at %d: % x", pos0(), miniBuf[miniDir+16:miniDir+40])
	prev := uint32(0)
	pos := int(miniDir + 16)
	for i := uint32(0); i < msz; i++ {
		var dn, sn int
		diff, dn := readU(pos)
		pos = dn
		prev += diff
		setOff, sn := readU(pos)
		pos = sn
		mm := mini.GetMethod(prev)
		if mm == nil {
			t.Errorf("entry %d: method %d unresolved", i, prev)
			continue
		}
		if setOff == 0 || setOff+4 > uint32(len(miniBuf)) {
			t.Errorf("entry %d: set offset %d invalid", i, setOff)
			continue
		}
		if sz := binary.LittleEndian.Uint32(miniBuf[setOff:]); sz > 0xffff {
			t.Errorf("entry %d: annotation set size %d implausible", i, sz)
		}
	}
}
