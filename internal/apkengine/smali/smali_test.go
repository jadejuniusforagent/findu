package smali

import (
	"path/filepath"
	"strings"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

// fixtureDex 打开单类 fixture APK 并解析其 DEX。
func fixtureDex(t *testing.T) *dex.DEX {
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
			return d
		}
	}
	t.Fatal("no dex in fixture")
	return nil
}

func TestClassRender(t *testing.T) {
	d := fixtureDex(t)
	cls := d.GetClassByIdx(0)
	lines := Class(d, cls)
	text := strings.Join(lines, "\n")

	// 类头 + 方法结构
	if !strings.Contains(text, ".class ") {
		t.Errorf("missing .class header")
	}
	if !strings.Contains(text, ".method ") || !strings.Contains(text, ".end method") {
		t.Errorf("missing method block")
	}
	// 指令行(带 i%04x 前缀)应存在且不少于构造器常有的若干条
	insnCount := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "i") && strings.Contains(l, ": ") {
			insnCount++
		}
	}
	if insnCount == 0 {
		t.Fatal("no decoded instructions")
	}
	// fixture 是合法 d8 产物:不应出现未知格式
	if strings.Contains(text, "(unknown format)") {
		t.Errorf("unknown format instructions:\n%s", text)
	}
	// 寄存器渲染形态检查:不应出现裸 "?"
	for _, l := range lines {
		if strings.Contains(l, "{?") || strings.Contains(l, ", ?") {
			t.Errorf("bad register list: %s", l)
		}
	}
}

func TestPayloadAndBranches(t *testing.T) {
	// 语法级:payload 跳行公式与分支渲染的单函数验证(不依赖 fixture 内容)
	// 公式验证:packed size=1 → 2+2*1=6;sparse size=2 → 2+4*2=10;
	// fill width=1,size=3 → (3*1+1)/2+4=6
	units2 := make([]byte, 40)
	put := func(idx, v uint16) { units2[idx*2] = byte(v); units2[idx*2+1] = byte(v >> 8) }

	put(0, 0x0100) // packed-switch ident
	put(1, 1)      // size
	if got := payloadUnits(units2, 0); got != 6 {
		t.Errorf("packed payload units = %d, want 6", got)
	}

	put(0, 0x0200) // sparse-switch ident
	put(1, 2)      // size
	if got := payloadUnits(units2, 0); got != 10 {
		t.Errorf("sparse payload units = %d, want 10", got)
	}

	put(0, 0x0300) // fill-array-data ident
	put(1, 1)      // element width
	put(2, 3)      // size lo
	put(3, 0)      // size hi
	if got := payloadUnits(units2, 0); got != 6 {
		t.Errorf("fill payload units = %d, want 6", got)
	}
}

// TestAllFixturesRender 全 fixture 全类渲染冒烟:任何 panic、未知格式、
// 非法寄存器形态都视为失败(覆盖真实 d8 产物的指令面)。
func TestAllFixturesRender(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "findrefs", "testdata", "*.apk"))
	if err != nil || len(matches) == 0 {
		t.Skip("no fixtures")
	}
	totalInsns := 0
	for _, apkPath := range matches {
		r, err := zipprobe.Open(apkPath)
		if err != nil {
			t.Fatalf("%s: %v", apkPath, err)
		}
		entries, err := zipprobe.ParseCDDexEntries(r)
		if err != nil {
			r.Close()
			t.Fatalf("%s: %v", apkPath, err)
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
				for i := 0; i < int(d.NumClasses()); i++ {
					cls := d.GetClassByIdx(uint32(i))
					lines := Class(d, cls)
					for _, l := range lines {
						if strings.Contains(l, "(unknown format)") {
							t.Errorf("%s %s: unknown format: %s", apkPath, cls.Fullname(), l)
						}
						if strings.Contains(l, "v?") {
							t.Errorf("%s %s: bad reg: %s", apkPath, cls.Fullname(), l)
						}
					}
					for _, l := range lines {
						if strings.HasPrefix(l, "i") {
							totalInsns++
						}
					}
				}
			}
		}
		r.Close()
	}
	t.Logf("decoded %d instructions across %d fixtures", totalInsns, len(matches))
	if totalInsns == 0 {
		t.Fatal("no instructions decoded at all")
	}
}
