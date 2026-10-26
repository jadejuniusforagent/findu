package godad

// switch_test — switch payload 解析与结构化测试。

import (
	"path/filepath"
	"strings"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

// buildPackedPayload 合成 packed-switch payload 字节(unit 数组)。
// 返回 payload 起始 unit 处的字节与长度。
func buildPackedPayload(keys []int32, targets []int) []byte {
	units := []uint16{0x0100, uint16(len(keys))}
	first := keys[0]
	units = append(units, uint16(uint32(first)), uint16(uint32(first)>>16))
	for _, t := range targets {
		units = append(units, uint16(uint32(t)), uint16(uint32(t)>>16))
	}
	out := make([]byte, len(units)*2)
	for i, u := range units {
		out[i*2] = byte(u)
		out[i*2+1] = byte(u >> 8)
	}
	return out
}

func buildSparsePayload(keys []int32, targets []int) []byte {
	units := []uint16{0x0200, uint16(len(keys))}
	for _, k := range keys {
		units = append(units, uint16(uint32(k)), uint16(uint32(k)>>16))
	}
	for _, t := range targets {
		units = append(units, uint16(uint32(t)), uint16(uint32(t)>>16))
	}
	out := make([]byte, len(units)*2)
	for i, u := range units {
		out[i*2] = byte(u)
		out[i*2+1] = byte(u >> 8)
	}
	return out
}

// TestParsePackedSwitchPayload 合成 payload:switch 在 unit 0,payload 在 unit 10。
func TestParsePackedSwitchPayload(t *testing.T) {
	// switch 指令:AA|op, offset 在 units 1-2(此处 switchPC=0, offset=10)
	units := []byte{
		0x00, 0x2b, // AA=0, op=packed-switch
		10, 0, 0, 0, // offset = 10 (units 1-2)
		0, 0, // padding(unit3,实际是下一条指令)
	}
	// 补足到 unit 10
	for len(units) < 20 {
		units = append(units, 0, 0)
	}
	// payload: keys {2,3,4}, targets 相对 switchPC={20,30,40}
	payload := buildPackedPayload([]int32{2, 3, 4}, []int{20, 30, 40})
	units = append(units, payload...)

	pl := parseSwitchPayload(units, len(units)/2, 0, 0x2b)
	if pl == nil {
		t.Fatal("parse returned nil")
	}
	if len(pl.Cases) != 3 {
		t.Fatalf("cases = %d, want 3", len(pl.Cases))
	}
	want := []SwitchCase{{2, 20}, {3, 30}, {4, 40}}
	for i, c := range pl.Cases {
		if c != want[i] {
			t.Errorf("case[%d] = %+v, want %+v", i, c, want[i])
		}
	}
	if pl.Default != 3 { // switchPC + Oplen(0x2b)=3
		t.Errorf("default = %d, want 3", pl.Default)
	}
}

// TestParseSparseSwitchPayload 合成 sparse payload。
func TestParseSparseSwitchPayload(t *testing.T) {
	units := make([]byte, 20) // units 0-9
	units[0] = 0x00
	units[1] = 0x2c // sparse-switch
	units[2] = 10   // offset = 10
	units[3], units[4] = 0, 0
	payload := buildSparsePayload([]int32{5, 100, -1}, []int{20, 12, 8})
	units = append(units, payload...)

	pl := parseSwitchPayload(units, len(units)/2, 0, 0x2c)
	if pl == nil {
		t.Fatal("parse returned nil")
	}
	if len(pl.Cases) != 3 {
		t.Fatalf("cases = %d, want 3", len(pl.Cases))
	}
	want := []SwitchCase{{5, 20}, {100, 12}, {-1, 8}}
	for i, c := range pl.Cases {
		if c != want[i] {
			t.Errorf("case[%d] = %+v, want %+v", i, c, want[i])
		}
	}
}

// TestParseSwitchPayloadInvalid 非法输入返回 nil。
func TestParseSwitchPayloadInvalid(t *testing.T) {
	// 全零:ident 不匹配
	units := make([]byte, 40)
	if pl := parseSwitchPayload(units, len(units)/2, 0, 0x2b); pl != nil {
		t.Errorf("invalid payload should return nil: %+v", pl)
	}
	// 越界偏移
	units2 := []byte{0x00, 0x2b, 200, 0, 0, 0}
	if pl := parseSwitchPayload(units2, len(units2)/2, 0, 0x2b); pl != nil {
		t.Errorf("out-of-range payload should return nil: %+v", pl)
	}
}

// TestStructureSwitchFixture 真实 fixture(XmlAssetReader)含 packed-switch:
// 应产出结构化 switch + case/default,而非注释。
func TestStructureSwitchFixture(t *testing.T) {
	apkPath := filepath.Join("..", "findrefs", "testdata", "XmlAssetReader.apk")
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	entries, _ := zipprobe.ParseCDDexEntries(r)
	var src string
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
			cls := d.GetClassByName("LXmlAssetReader;")
			if cls == nil {
				continue
			}
			src = New(d).DecompileClass(cls)
		}
	}
	if src == "" {
		t.Fatal("class not decompiled")
	}
	for _, want := range []string{"switch (", "case 2:", "case 3:", "case 4:", "default:"} {
		if !strings.Contains(src, want) {
			t.Errorf("switch output missing %q:\n%s", want, src)
		}
	}
	// 不应出现旧的近似注释
	if strings.Contains(src, "按顺序近似") || strings.Contains(src, "no convergence point") {
		t.Errorf("switch fell back to approximation:\n%s", src)
	}
	// case 2 内嵌嵌套循环
	if !strings.Contains(src, "while (") {
		t.Errorf("nested loop in case missing:\n%s", src)
	}
}

// TestSwitchAllFixtures 全 fixture 含 switch 的方法全部结构化冒烟。
func TestSwitchAllFixtures(t *testing.T) {
	matches, _ := filepath.Glob(filepath.Join("..", "findrefs", "testdata", "*.apk"))
	total := 0
	for _, apkPath := range matches {
		r, err := zipprobe.Open(apkPath)
		if err != nil {
			continue
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
				dec := New(d)
				for i := 0; i < int(d.NumClasses()); i++ {
					cls := d.GetClassByIdx(uint32(i))
					src := dec.DecompileClass(cls)
					if strings.Contains(src, "aborted") {
						t.Errorf("%s %s: aborted", apkPath, cls.Fullname())
					}
					total++
				}
			}
		}
		r.Close()
	}
	t.Logf("decompiled %d classes", total)
}
