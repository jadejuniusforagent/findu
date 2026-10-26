package pluginhost

import (
	"context"
	"errors"
	"testing"
)

type fakePlugin struct {
	m         Manifest
	startErr  error
	stopErr   error
	healthErr error
	starts    int
	stops     int
	healths   int
}

func (f *fakePlugin) Manifest() Manifest { return f.m }

func (f *fakePlugin) Start(context.Context) error {
	f.starts++
	return f.startErr
}

func (f *fakePlugin) Stop(context.Context) error {
	f.stops++
	return f.stopErr
}

func (f *fakePlugin) Health(context.Context) error {
	f.healths++
	return f.healthErr
}

func testManifest(id string, mutate ...func(*Manifest)) Manifest {
	m := Manifest{
		ID:         id,
		Version:    "1.0.0",
		Kind:       KindTool,
		APIVersion: APIVersion,
		Provides:   &Provides{},
	}
	for _, fn := range mutate {
		fn(&m)
	}
	return m
}

func TestRegistryRegisterValidates(t *testing.T) {
	r := NewRegistry("")

	if err := r.Register(&fakePlugin{m: testManifest("intel", func(m *Manifest) {
		m.OwnsTables = []string{"vuln_kb", "fingerprint_rules"}
	})}); err != nil {
		t.Fatalf("intel 应注册成功: %v", err)
	}
	if got := r.APIVersion(); got != APIVersion {
		t.Fatalf("APIVersion=%q", got)
	}
	if _, ok := r.Get("intel"); !ok {
		t.Fatal("Get 应返回已注册插件")
	}

	cases := []struct {
		name string
		p    Plugin
		want error
	}{
		{"nil", nil, ErrInvalidManifest},
		{"重复 id", &fakePlugin{m: testManifest("intel")}, ErrDuplicatePlugin},
		{"API 版本不一致", &fakePlugin{m: testManifest("c2", func(m *Manifest) {
			m.APIVersion = "2.0"
		})}, ErrInvalidManifest},
		{"requires.api 不一致", &fakePlugin{m: testManifest("c2", func(m *Manifest) {
			m.Requires = &Requires{API: "9.9"}
		})}, ErrInvalidManifest},
		{"声明 domain 表", &fakePlugin{m: testManifest("intel", func(m *Manifest) {
			m.OwnsTables = []string{"assets"}
		})}, ErrTableOwnership},
		{"声明别的插件的表", &fakePlugin{m: testManifest("intel", func(m *Manifest) {
			m.OwnsTables = []string{"c2_events"}
		})}, ErrTableOwnership},
		{"声明未登记表", &fakePlugin{m: testManifest("intel", func(m *Manifest) {
			m.OwnsTables = []string{"no_such_table"}
		})}, ErrTableOwnership},
		{"清单非法", &fakePlugin{m: Manifest{ID: "X"}}, ErrInvalidManifest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := r.Register(c.p)
			if !errors.Is(err, c.want) {
				t.Fatalf("Register 错误 = %v，期望 %v", err, c.want)
			}
		})
	}

	if got := len(r.List()); got != 1 {
		t.Fatalf("失败注册不应改变注册表，当前 %d 个", got)
	}
}

func TestRegistryLifecycleAndDependencies(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry("")

	a := &fakePlugin{m: testManifest("c2", func(m *Manifest) {
		m.OwnsTables = []string{"c2_sessions"}
	})}
	b := &fakePlugin{m: testManifest("intel", func(m *Manifest) {
		m.Requires = &Requires{Plugins: []string{"c2"}}
	})}
	if err := r.Register(b); err != nil {
		t.Fatalf("先注册依赖方失败: %v", err)
	}
	if err := r.Register(a); err != nil {
		t.Fatalf("注册被依赖方失败: %v", err)
	}

	// 只注册未启动，依赖不满足
	if err := r.Start(ctx, "intel"); !errors.Is(err, ErrDependency) {
		t.Fatalf("依赖未启动应返回 ErrDependency，得到 %v", err)
	}
	if err := r.Start(ctx, "c2"); err != nil {
		t.Fatalf("启动 c2 失败: %v", err)
	}
	if a.starts != 1 {
		t.Fatalf("c2 Start 调用次数 %d", a.starts)
	}
	if err := r.Start(ctx, "intel"); err != nil {
		t.Fatalf("启动 intel 失败: %v", err)
	}
	if err := r.Start(ctx, "intel"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("重复启动应拒绝，得到 %v", err)
	}

	// 停止被依赖方必须失败
	if err := r.Stop(ctx, "c2"); !errors.Is(err, ErrInUse) {
		t.Fatalf("仍被依赖时 Stop 应返回 ErrInUse，得到 %v", err)
	}
	if a.stops != 0 {
		t.Fatal("被拒绝的 Stop 不应调用插件")
	}
	if err := r.Unregister("c2"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("运行中 Unregister 应返回 ErrInvalidState，得到 %v", err)
	}

	if err := r.Stop(ctx, "intel"); err != nil {
		t.Fatalf("停止 intel 失败: %v", err)
	}
	if err := r.Stop(ctx, "c2"); err != nil {
		t.Fatalf("停止 c2 失败: %v", err)
	}
	if a.stops != 1 || b.stops != 1 {
		t.Fatalf("Stop 调用次数 c2=%d intel=%d", a.stops, b.stops)
	}
	// intel 虽已停止但仍在注册表中声明依赖 c2，注销 c2 必须拒绝
	if err := r.Unregister("c2"); !errors.Is(err, ErrInUse) {
		t.Fatalf("已停止但被依赖时 Unregister 应返回 ErrInUse，得到 %v", err)
	}
	if err := r.Start(ctx, "intel"); !errors.Is(err, ErrDependency) {
		t.Fatalf("依赖已停止时应拒绝启动，得到 %v", err)
	}

	// 停止后可注销；注销后不再出现在列表
	if err := r.Unregister("intel"); err != nil {
		t.Fatalf("注销 intel 失败: %v", err)
	}
	if err := r.Unregister("c2"); err != nil {
		t.Fatalf("注销 c2 失败: %v", err)
	}
	if got := r.List(); len(got) != 0 {
		t.Fatalf("注销后列表应为空，得到 %+v", got)
	}
	if err := r.Unregister("c2"); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("重复注销应返回 ErrPluginNotFound，得到 %v", err)
	}
}

func TestRegistryStatesAndErrors(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry("")

	boom := errors.New("boom")
	p := &fakePlugin{m: testManifest("intel"), healthErr: boom}
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}

	if err := r.Health(ctx, "intel"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("未启动探活应返回 ErrInvalidState，得到 %v", err)
	}
	if _, ok := r.Status("intel"); !ok {
		t.Fatal("Status 应存在")
	}
	if got, _ := r.Status("intel"); got.State != StateRegistered {
		t.Fatalf("初始状态 %s", got.State)
	}

	p.startErr = errors.New("cannot start")
	if err := r.Start(ctx, "intel"); err == nil {
		t.Fatal("启动失败应返回错误")
	}
	st, _ := r.Status("intel")
	if st.State != StateFailed || st.Error != "cannot start" {
		t.Fatalf("失败状态未记录: %+v", st)
	}

	p.startErr = nil
	if err := r.Start(ctx, "intel"); err != nil {
		t.Fatalf("失败后应允许重试: %v", err)
	}
	if err := r.Health(ctx, "intel"); !errors.Is(err, boom) {
		t.Fatalf("健康检查错误应透传: %v", err)
	}
	if p.healths != 1 {
		t.Fatalf("Health 调用次数 %d", p.healths)
	}

	if err := r.Unregister("intel"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("运行中注销应拒绝，得到 %v", err)
	}
}

func TestRegistryListKeepsRegistrationOrder(t *testing.T) {
	r := NewRegistry("")
	for _, id := range []string{"c2", "intel", "notify"} {
		if err := r.Register(&fakePlugin{m: testManifest(id)}); err != nil {
			t.Fatal(err)
		}
	}
	list := r.List()
	want := []string{"c2", "intel", "notify"}
	if len(list) != len(want) {
		t.Fatalf("List 长度 %d", len(list))
	}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("List[%d]=%s，期望 %s", i, list[i].ID, id)
		}
	}
}
