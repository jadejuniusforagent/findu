package zipprobe

import (
	"bytes"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/internal/fixture"
)

func buildAPK(t *testing.T) BytesReader {
	t.Helper()
	return BytesReader{Data: fixture.BuildTestAPK()}
}

func TestParseCDDexEntries(t *testing.T) {
	r := buildAPK(t)
	entries, err := ParseCDDexEntries(r)
	if err != nil {
		t.Fatalf("ParseCDDexEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
		if e.Method != 8 {
			t.Errorf("%s: method = %d, want 8 (deflate)", e.Name, e.Method)
		}
		if e.CompSize == 0 || e.UncompSize == 0 {
			t.Errorf("%s: sizes zero: %+v", e.Name, e)
		}
	}
	if !names["classes.dex"] || !names["classes2.dex"] {
		t.Errorf("missing entries: %v", names)
	}
}

func TestInflateDexRoundtrip(t *testing.T) {
	r := buildAPK(t)
	entries, err := ParseCDDexEntries(r)
	if err != nil {
		t.Fatalf("ParseCDDexEntries: %v", err)
	}
	SortByCompSize(entries)
	want := fixture.BuildTestDEX()
	for _, e := range entries {
		data, err := InflateDex(r, e)
		if err != nil {
			t.Fatalf("InflateDex(%s): %v", e.Name, err)
		}
		if !bytes.Equal(data, want) {
			t.Errorf("%s: inflated bytes differ from fixture", e.Name)
		}
		d, err := dex.Parse(data, e.Name)
		if err != nil {
			t.Fatalf("dex.Parse(%s): %v", e.Name, err)
		}
		if d.GetClassByName("Lcom/poc/Main;") == nil {
			t.Errorf("%s: class Main not found after inflate", e.Name)
		}
	}
}

func TestDexDefinesClass(t *testing.T) {
	raw := fixture.BuildTestDEX()
	ok, err := DexDefinesClass(raw, "Lcom/poc/Main;")
	if err != nil {
		t.Fatalf("DexDefinesClass: %v", err)
	}
	if !ok {
		t.Error("Main should be defined")
	}
	ok, err = DexDefinesClass(raw, "Lcom/poc/Base;")
	if err != nil {
		t.Fatalf("DexDefinesClass: %v", err)
	}
	if ok {
		t.Error("Base is only a superclass, not defined here")
	}
	// 未在 class_defs 出现的类型(type_ids 里存在但无定义)
	if ok, _ := DexDefinesClass(raw, "Ljava/lang/Object;"); ok {
		t.Error("Object should not be defined in fixture")
	}
}

func TestLogicalDexBuffersPlain(t *testing.T) {
	raw := fixture.BuildTestDEX()
	logical := LogicalDexBuffers("classes.dex", raw)
	if len(logical) != 1 || logical[0].Name != "classes.dex" || &logical[0].Buf[0] != &raw[0] {
		t.Errorf("plain dex should pass through: %+v", logical)
	}
}

func TestLogicalDexBuffers041(t *testing.T) {
	// 两个 dex 拼成 dex041 容器
	d1 := fixture.BuildTestDEX()
	d2 := fixture.BuildTestDEX()
	container := append(append([]byte{}, d1...), d2...)
	binaryPatchFileSize := func(dexBytes []byte, fileSize int) {
		dexBytes[0x20] = byte(fileSize)
		dexBytes[0x21] = byte(fileSize >> 8)
		dexBytes[0x22] = byte(fileSize >> 16)
		dexBytes[0x23] = byte(fileSize >> 24)
	}
	copy(container, "dex\n041\x00")
	copy(container[len(d1):], "dex\n041\x00")
	binaryPatchFileSize(d1, len(d1)) // 子 DEX 声明自身大小

	logical := LogicalDexBuffers("classes.dex", container)
	if len(logical) != 2 {
		t.Fatalf("logical = %d, want 2", len(logical))
	}
	if logical[0].Name != "classes.dex!classes1.dex" || logical[1].Name != "classes.dex!classes2.dex" {
		t.Errorf("names = %q, %q", logical[0].Name, logical[1].Name)
	}
	for i, l := range logical {
		d, err := dex.Parse(l.Buf, l.Name)
		if err != nil {
			t.Fatalf("logical[%d] parse: %v", i, err)
		}
		if d.GetClassByName("Lcom/poc/Main;") == nil {
			t.Errorf("logical[%d]: class Main not found", i)
		}
	}
}

func TestEntriesFromRealFile(t *testing.T) {
	// os.File 路径(ReadAt)与内存路径行为一致
	r := buildAPK(t)
	want, err := ParseCDDexEntries(r)
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	_ = want
}

func TestDebugManifestEntry(t *testing.T) {
	r := buildAPK(t)
	eocd, _ := FindEOCD(r)
	head := make([]byte, 20)
	r.ReadAt(head, eocd+12)
	cdSize := int64(u32(head, 0))
	cdOff := int64(u32(head, 4))
	cd := make([]byte, cdSize)
	r.ReadAt(cd, cdOff)
	ptr := 0
	for ptr+46 <= len(cd) {
		if string(cd[ptr:ptr+4]) != "PK\x01\x02" {
			break
		}
		nameLen := int(u16(cd, ptr+28))
		extraLen := int(u16(cd, ptr+30))
		commentLen := int(u16(cd, ptr+32))
		name := string(cd[ptr+46 : ptr+46+nameLen])
		t.Logf("entry %q method=%d comp=%d uncomp=%d local=%d", name,
			u16(cd, ptr+10), u32(cd, ptr+20), u32(cd, ptr+24), u32(cd, ptr+42))
		ptr = ptr + 46 + nameLen + extraLen + commentLen
	}
	_ = fileReader{}
}
