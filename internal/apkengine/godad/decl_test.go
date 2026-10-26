package godad

// decl_test — 变量声明测试:版本内联声明、类型推断、move 链与参数类型
// 传播、作用域检查失败的残余顶部声明、合并组声明。版本重命名本身由
// reachdef_test 覆盖(块级分析),这里通过手动设置 Var.Name 模拟其产物。

import (
	"strings"
	"testing"
)

func TestInferType(t *testing.T) {
	cases := []struct {
		node Node
		want string
	}{
		{&NewObj{Desc: "Lcom/Foo;"}, "com.Foo"},
		{&StringConst{V: "s"}, "String"},
		{&NewArray{Type: "[B", Size: &IntConst{V: 1}}, "byte[]"},
		{&ClassConst{Desc: "Lcom/Foo;"}, "Class"},
		{&IntConst{V: 5}, "int"},
		{&LongConst{V: 5}, "long"},
		{&Compare{Op: "==", LHS: &IntConst{}, RHS: &IntConst{}}, "boolean"},
		{&Invoke{Name: "x", RetType: "Ljava/lang/String;"}, "java.lang.String"},
		{&Invoke{Name: "x", RetType: "V"}, ""},
		{&Unresolved{Op: "x"}, ""},
	}
	for _, c := range cases {
		if got := inferType(c.node); got != c.want {
			t.Errorf("inferType(%T) = %q, want %q", c.node, got, c.want)
		}
	}
}

func TestDeclareVarsInline(t *testing.T) {
	// 独占版本(LHS Name 非空)→ 内联声明 com.Foo v0 = new com.Foo();
	nodes := []Node{
		&Assign{LHS: &Var{Reg: 0, Name: "v0"}, RHS: &NewObj{Desc: "Lcom/Foo;"}},
		&Invoke{Name: "bar", This: &Var{Reg: 0, Name: "v0"}, RetType: "V"},
		&ReturnVal{},
	}
	out := DeclareVars(nodes, nil, nil)
	if len(out) != 3 {
		t.Fatalf("nodes = %d, want 3 (inline decl, no top DeclStmt)", len(out))
	}
	assign, ok := out[0].(*Assign)
	if !ok {
		t.Fatalf("first node = %T, want Assign", out[0])
	}
	if assign.DeclType != "com.Foo" {
		t.Errorf("DeclType = %q, want com.Foo", assign.DeclType)
	}
	w := NewWriter()
	w.WriteNode(assign)
	if got := w.String(); !strings.Contains(got, "com.Foo v0 = new com.Foo();") {
		t.Errorf("inline decl render = %q", got)
	}
	for _, n := range out {
		if _, isDecl := n.(*DeclStmt); isDecl {
			t.Errorf("top-level DeclStmt should not exist for top-level first def")
		}
	}
}

func TestDeclareVarsSkipsParams(t *testing.T) {
	// this/argN 寄存器被写入也不应内联声明
	params := map[int]string{8: "this", 9: "arg0"}
	paramTypes := map[int]string{8: "com.Foo", 9: "int"}
	nodes := []Node{
		&Assign{LHS: &Var{Reg: 9, Name: "arg0"}, RHS: &IntConst{V: 1}},
		&Assign{LHS: &Var{Reg: 8, Name: "this"}, RHS: &Var{Reg: 1, Name: "v1"}},
	}
	DeclareVars(nodes, params, paramTypes)
	for _, n := range nodes {
		if a, ok := n.(*Assign); ok && a.DeclType != "" {
			t.Errorf("param register declared inline: %s %s", a.DeclType, a.LHS.Java())
		}
	}
}

func TestDeclareVarsScopeFailResidual(t *testing.T) {
	// 定义在 IfStmt 体内、使用在体外:作用域检查失败 → 顶部残余声明,
	// 插在 super() 之后;嵌套定义处不得内联(避免重复声明)
	params := map[int]string{8: "this"}
	lv := &Var{Reg: 0, Name: "v0"}
	use := &Var{Reg: 0, Name: "v0"} // versionRegs 重命名后的引用
	nodes := []Node{
		&Invoke{Name: "<init>", CtorCall: "super", This: &Var{Reg: 8, Name: "this"}},
		&IfStmt{Cond: &BoolConst{V: true}, Then: []Node{
			&Assign{LHS: lv, RHS: &NewObj{Desc: "Lcom/Baz;"}},
		}},
		&Invoke{Name: "take", Args: []Node{use}, RetType: "V"},
		&ReturnVal{},
	}
	out := DeclareVars(nodes, params, nil)
	if len(out) != 5 {
		t.Fatalf("nodes = %d, want 5 (decl + 4)", len(out))
	}
	if inv, ok := out[0].(*Invoke); !ok || inv.CtorCall != "super" {
		t.Errorf("first node should remain super(), got %T", out[0])
	}
	decl, ok := out[1].(*DeclStmt)
	if !ok {
		t.Fatalf("decl should be inserted after super(), got %T", out[1])
	}
	if !strings.Contains(decl.Java(), "com.Baz v0;") {
		t.Errorf("decl Java = %q, want com.Baz v0;", decl.Java())
	}
	ifstmt := out[2].(*IfStmt)
	if a, ok := ifstmt.Then[0].(*Assign); ok && a.DeclType != "" {
		t.Errorf("scope-failed def should not be inline declared: %q", a.DeclType)
	}
}

func TestDeclareVarsMovePropagation(t *testing.T) {
	// v0 = new Foo(); v1 = v0; → 两版本均内联,v1 类型经 move 链传播
	nodes := []Node{
		&Assign{LHS: &Var{Reg: 0, Name: "v0"}, RHS: &NewObj{Desc: "Lcom/Foo;"}},
		&Assign{LHS: &Var{Reg: 1, Name: "v1"}, RHS: &Var{Reg: 0, Name: "v0"}},
	}
	out := DeclareVars(nodes, nil, nil)
	if len(out) != 2 {
		t.Fatalf("nodes = %d, want 2", len(out))
	}
	if a := out[0].(*Assign); a.DeclType != "com.Foo" {
		t.Errorf("v0 DeclType = %q, want com.Foo", a.DeclType)
	}
	if a := out[1].(*Assign); a.DeclType != "com.Foo" {
		t.Errorf("v1 (move) DeclType = %q, want com.Foo", a.DeclType)
	}
}

func TestDeclareVarsParamTypePropagation(t *testing.T) {
	// v0 = arg0(String 参数)→ v0 内联声明 String
	params := map[int]string{3: "arg0"}
	paramTypes := map[int]string{3: "String"}
	nodes := []Node{
		&Assign{LHS: &Var{Reg: 0, Name: "v0"}, RHS: &Var{Reg: 3, Name: "arg0"}},
	}
	out := DeclareVars(nodes, params, paramTypes)
	if a := out[0].(*Assign); a.DeclType != "String" {
		t.Errorf("DeclType = %q, want String (param move propagation)", a.DeclType)
	}
}

func TestDeclareVarsMergedGrouping(t *testing.T) {
	// 合并组(Name == "")按类型在顶部合并一行
	nodes := []Node{
		&Assign{LHS: &Var{Reg: 0}, RHS: &IntConst{V: 1}},
		&Assign{LHS: &Var{Reg: 1}, RHS: &IntConst{V: 2}},
		&Assign{LHS: &Var{Reg: 2}, RHS: &StringConst{V: "s"}},
	}
	out := DeclareVars(nodes, nil, nil)
	decl := out[0].(*DeclStmt)
	if len(decl.Decls) != 2 {
		t.Fatalf("decl groups = %d, want 2: %+v", len(decl.Decls), decl.Decls)
	}
	if decl.Decls[0].Type != "int" || len(decl.Decls[0].Names) != 2 {
		t.Errorf("int group = %+v", decl.Decls[0])
	}
	if decl.Decls[1].Type != "String" || decl.Decls[1].Names[0] != "v2" {
		t.Errorf("String group = %+v", decl.Decls[1])
	}
}

func TestDeclareVarsFixture(t *testing.T) {
	// fixture 类应产出首次使用处声明(ByteArrayOutputStream 构造)
	d, cls := fixtureClass(t, "LByteStreamReader;")
	src := New(d).DecompileClass(cls)
	if !strings.Contains(src, "java.io.ByteArrayOutputStream v0 = new java.io.ByteArrayOutputStream();") {
		t.Errorf("expected inline declaration for v0:\n%s", src)
	}
	if strings.Contains(src, "aborted") {
		t.Errorf("decompilation aborted:\n%s", src)
	}
}
