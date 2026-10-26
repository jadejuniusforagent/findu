package godad

// godad_test — Go DAD 单元测试:类型渲染、构造器折叠、参数映射、
// 真实 fixture 类反编译(构造器/方法/控制流注释)。

import (
	"path/filepath"
	"strings"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

func TestJavaType(t *testing.T) {
	cases := map[string]string{
		"V":                   "void",
		"I":                   "int",
		"Z":                   "boolean",
		"J":                   "long",
		"[B":                  "byte[]",
		"[[I":                 "int[][]",
		"Ljava/lang/String;":  "java.lang.String",
		"Lcom/foo/Bar;":       "com.foo.Bar",
		"[Ljava/lang/Object;": "java.lang.Object[]",
	}
	for in, want := range cases {
		if got := JavaType(in); got != want {
			t.Errorf("JavaType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFoldConstructors(t *testing.T) {
	// v0 = new Foo(); invoke-direct {v0}, Foo.<init>(v1) → v0 = new Foo(v1)
	v0 := &Var{Reg: 0}
	nodes := []Node{
		&Assign{LHS: v0, RHS: &NewObj{Desc: "Lcom/Foo;"}},
		&Invoke{Type: InvokeDirect, Owner: "Lcom/Foo;", Name: "<init>", This: &Var{Reg: 0}, Args: []Node{&Var{Reg: 1}}},
	}
	out := foldConstructors(nodes)
	if len(out) != 1 {
		t.Fatalf("folded nodes = %d, want 1", len(out))
	}
	assign := out[0].(*Assign)
	no := assign.RHS.(*NewObj)
	if len(no.Args) != 1 {
		t.Errorf("ctor args = %d, want 1", len(no.Args))
	}
	if !strings.Contains(no.Java(), "new com.Foo(v1)") {
		t.Errorf("folded Java = %q", no.Java())
	}

	// 不匹配的 invoke(不同寄存器)不应折叠
	nodes2 := []Node{
		&Assign{LHS: &Var{Reg: 0}, RHS: &NewObj{Desc: "Lcom/Foo;"}},
		&Invoke{Type: InvokeDirect, Owner: "Lcom/Foo;", Name: "<init>", This: &Var{Reg: 9}},
	}
	if got := foldConstructors(nodes2); len(got) != 2 {
		t.Errorf("non-matching ctor should not fold: %d nodes", len(got))
	}
}

func TestInvokeCtorCallRendering(t *testing.T) {
	inv := &Invoke{Name: "<init>", CtorCall: "super", Args: []Node{&IntConst{V: 1}}}
	if got := inv.Java(); got != "super(1)" {
		t.Errorf("CtorCall render = %q, want super(1)", got)
	}
	inv2 := &Invoke{Name: "<init>", CtorCall: "this"}
	if got := inv2.Java(); got != "this()" {
		t.Errorf("CtorCall render = %q, want this()", got)
	}
}

// fixtureClass 打开 fixture APK 并解析目标类。
func fixtureClass(t *testing.T, classDesc string) (*dex.DEX, *dex.Class) {
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

func TestDecompileFixtureClass(t *testing.T) {
	d, cls := fixtureClass(t, "LByteStreamReader;")
	src := New(d).DecompileClass(cls)

	// 类头/父类
	if !strings.Contains(src, "class ByteStreamReader extends java.lang.Object") {
		t.Errorf("class header missing:\n%s", src)
	}
	// 构造器与 super() 识别
	if !strings.Contains(src, "public void <init>()") {
		t.Errorf("constructor missing:\n%s", src)
	}
	if !strings.Contains(src, "super();") {
		t.Errorf("super() call not recognized:\n%s", src)
	}
	// 方法签名(byte[] 返回类型 + 参数名)
	if !strings.Contains(src, "byte[] read(java.io.InputStream arg0)") {
		t.Errorf("method signature unexpected:\n%s", src)
	}
	// 未实现的操作码应有注释标记,而不是崩溃
	if strings.Contains(src, "panic") {
		t.Errorf("output contains panic marker")
	}
}

func TestDecompileAllFixtureClasses(t *testing.T) {
	// 全 fixture 类反编译冒烟:任何 panic / 空输出视为失败
	matches, err := filepath.Glob(filepath.Join("..", "findrefs", "testdata", "*.apk"))
	if err != nil || len(matches) == 0 {
		t.Skip("no fixtures")
	}
	total := 0
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
				dec := New(d)
				for i := 0; i < int(d.NumClasses()); i++ {
					cls := d.GetClassByIdx(uint32(i))
					src := dec.DecompileClass(cls)
					if !strings.Contains(src, "class") {
						t.Errorf("%s %s: empty class output", apkPath, cls.Fullname())
					}
					total++
				}
			}
		}
		r.Close()
	}
	t.Logf("decompiled %d classes across fixtures", total)
	if total == 0 {
		t.Fatal("no classes decompiled")
	}
}

func TestParamNames(t *testing.T) {
	d, cls := fixtureClass(t, "LByteStreamReader;")
	_ = d
	for _, m := range cls.Methods() {
		code := m.Code()
		if code == nil {
			continue
		}
		names := paramNames(m, code)
		isStatic := m.AccessFlags&0x8 != 0
		if len(names) != int(code.InsSize) {
			t.Errorf("%s: param names %d != ins size %d", m.Name(), len(names), code.InsSize)
		}
		// 实例方法第一个参数必须是 this
		if !isStatic {
			first := int(code.RegistersSize) - int(code.InsSize)
			if names[first] != "this" {
				t.Errorf("%s: first param = %q, want this", m.Name(), names[first])
			}
		}
	}
}
