package provider_test

import (
	"context"
	"errors"
	"testing"

	"finduai/core/provider"
	"finduai/core/provider/mock"
)

// c2RoundTrip 是只依赖 C2Provider 接口的调用链：换实现时这段代码不变。
func c2RoundTrip(t *testing.T, p provider.C2Provider, addSession func(listenerID string)) {
	t.Helper()
	ctx := context.Background()

	l, err := p.StartListener(ctx, provider.ListenerSpec{Name: "t", Address: "127.0.0.1:4444", Protocol: "tcp"})
	if err != nil {
		t.Fatalf("StartListener: %v", err)
	}
	if l.Status != "running" {
		t.Fatalf("listener status=%s", l.Status)
	}
	list, err := p.ListListeners(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListListeners=%v err=%v", list, err)
	}

	addSession(l.ID)
	sessions, err := p.ListSessions(ctx, provider.SessionFilter{ListenerID: l.ID, Status: "active"})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions=%v err=%v", sessions, err)
	}

	task, err := p.Dispatch(ctx, provider.TaskSpec{SessionID: sessions[0].ID, Command: "id"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if task.State != provider.TaskCompleted {
		t.Fatalf("task state=%s", task.State)
	}
	polled, err := p.PollTask(ctx, task.ID)
	if err != nil || polled.ID != task.ID {
		t.Fatalf("PollTask=%v err=%v", polled, err)
	}
	res, err := p.TaskResult(ctx, task.ID)
	if err != nil || res.Stdout == "" {
		t.Fatalf("TaskResult=%+v err=%v", res, err)
	}

	pay, err := p.BuildPayload(ctx, provider.PayloadSpec{Name: "beacon.exe", Format: "exe", Host: "10.0.0.5"})
	if err != nil || pay.SHA256 == "" {
		t.Fatalf("BuildPayload=%+v err=%v", pay, err)
	}
	payloads, err := p.ListPayloads(ctx)
	if err != nil || len(payloads) != 1 {
		t.Fatalf("ListPayloads=%v err=%v", payloads, err)
	}

	ch, err := p.Events(ctx, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	kinds := map[string]bool{}
	for e := range ch {
		kinds[e.Kind] = true
	}
	for _, want := range []string{provider.EventC2ListenerStarted, provider.EventC2SessionOpened,
		provider.EventC2TaskDispatched, provider.EventC2TaskCompleted} {
		if !kinds[want] {
			t.Fatalf("缺少事件 %s，实际 %v", want, kinds)
		}
	}

	if err := p.KillSession(ctx, sessions[0].ID); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	s, err := p.GetSession(ctx, sessions[0].ID)
	if err != nil || s.Status != "closed" {
		t.Fatalf("KillSession 后 session=%+v err=%v", s, err)
	}
	if err := p.StopListener(ctx, l.ID); err != nil {
		t.Fatalf("StopListener: %v", err)
	}
}

func TestC2Contract(t *testing.T) {
	seed := func(m *mock.C2) func(string) {
		return func(listenerID string) {
			// 只有 mock 有注入入口；真实实现靠回连事件。
			m.AddSession(provider.Session{ListenerID: listenerID, Host: "10.0.0.5", User: "www"})
		}
	}
	base := mock.NewC2()
	c2RoundTrip(t, base, seed(base))

	// 定制结果：同一个接口、不同行为，调用链不变。
	custom := mock.NewC2()
	custom.SetResponder(func(spec provider.TaskSpec) provider.TaskResult {
		return provider.TaskResult{Stdout: "custom:" + spec.Command, ExitCode: 7}
	})
	c2RoundTrip(t, custom, seed(custom))
}

func TestWebShellContract(t *testing.T) {
	ctx := context.Background()
	w := mock.NewWebShell()

	conn, err := w.Open(ctx, provider.ConnSpec{URL: "https://target/shell.php", Password: "x"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if h, err := w.Test(ctx, conn.ID); err != nil || !h.OK {
		t.Fatalf("Test=%+v err=%v", h, err)
	}
	if res, err := w.Exec(ctx, conn.ID, "id"); err != nil || res.Stdout != "mock: id" {
		t.Fatalf("Exec=%+v err=%v", res, err)
	}

	// 默认实现读取的是 memory 文件表，空文件应报 ErrNotFound。
	if _, err := w.ReadFile(ctx, conn.ID, "/tmp/a"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("ReadFile missing err=%v", err)
	}
	if err := w.WriteFile(ctx, conn.ID, "/tmp/a", []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if b, err := w.ReadFile(ctx, conn.ID, "/tmp/a"); err != nil || string(b) != "hello" {
		t.Fatalf("ReadFile=%q err=%v", b, err)
	}
	files, err := w.ListFiles(ctx, conn.ID, "/tmp")
	if err != nil || len(files) != 1 || files[0].Name != "a" {
		t.Fatalf("ListFiles=%+v err=%v", files, err)
	}
	if err := w.Upload(ctx, conn.ID, "/tmp/b", []byte("uploaded")); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if err := w.DeleteFile(ctx, conn.ID, "/tmp/b"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if err := w.Close(ctx, conn.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if h, err := w.Test(ctx, conn.ID); err != nil || h.OK {
		t.Fatalf("关闭后 Test=%+v err=%v", h, err)
	}
	if _, err := w.Exec(ctx, conn.ID, "id"); !errors.Is(err, provider.ErrConflict) {
		t.Fatalf("关闭后 Exec err=%v", err)
	}
}

func TestTrafficContract(t *testing.T) {
	ctx := context.Background()
	tr := mock.NewTraffic()

	cap, err := tr.StartCapture(ctx, provider.CaptureFilter{Host: "a.example"})
	if err != nil || cap.Status != "running" {
		t.Fatalf("StartCapture=%+v err=%v", cap, err)
	}
	f := tr.AddFlow(provider.Flow{
		CaptureID: cap.ID, Method: "GET", URL: "https://a.example/api/x", Host: "a.example",
		StatusCode: 200, RequestBody: []byte("q=1"),
	})
	if f.ID == "" || f.CapturedAt.IsZero() {
		t.Fatalf("AddFlow 未补全字段: %+v", f)
	}

	got, err := tr.Query(ctx, provider.FlowCriteria{Method: "get", Host: "a.example", URLContains: "/api/"})
	if err != nil || len(got) != 1 || got[0].ID != f.ID {
		t.Fatalf("Query=%+v err=%v", got, err)
	}
	if got, _ := tr.Query(ctx, provider.FlowCriteria{Method: "POST"}); len(got) != 0 {
		t.Fatalf("method 过滤失效: %+v", got)
	}
	if one, err := tr.Get(ctx, f.ID); err != nil || one.URL != f.URL {
		t.Fatalf("Get=%+v err=%v", one, err)
	}

	rep, err := tr.Replay(ctx, f.ID, provider.Mutation{Method: "POST", Body: []byte("x=2")})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if rep.ID == f.ID || rep.Method != "POST" || string(rep.RequestBody) != "x=2" {
		t.Fatalf("Replay 结果不对: %+v", rep)
	}
	if all, _ := tr.Query(ctx, provider.FlowCriteria{}); len(all) != 2 {
		t.Fatalf("重放后应有 2 条 flow，得到 %d", len(all))
	}

	if err := tr.StopCapture(ctx, cap.ID); err != nil {
		t.Fatalf("StopCapture: %v", err)
	}
	if _, err := tr.Get(ctx, "missing"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Get missing err=%v", err)
	}
}

// 负路径：能力枚举、缺失资源、关闭后的会话/连接都应返回可判定错误。
func TestContractErrors(t *testing.T) {
	ctx := context.Background()

	if provider.Capability("dns").Valid() {
		t.Fatal("未知能力不应通过 Valid")
	}
	for _, c := range []provider.Capability{provider.CapabilityC2, provider.CapabilityWebShell, provider.CapabilityTraffic} {
		if !c.Valid() {
			t.Fatalf("%s 应合法", c)
		}
	}

	c2 := mock.NewC2()
	if err := c2.StopListener(ctx, "missing"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("StopListener missing err=%v", err)
	}
	if _, err := c2.Dispatch(ctx, provider.TaskSpec{SessionID: "missing", Command: "id"}); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Dispatch missing session err=%v", err)
	}
	if _, err := c2.GetSession(ctx, "missing"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("GetSession missing err=%v", err)
	}
	if _, err := c2.TaskResult(ctx, "missing"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("TaskResult missing err=%v", err)
	}

	ws := mock.NewWebShell()
	if _, err := ws.Open(ctx, provider.ConnSpec{}); !errors.Is(err, provider.ErrConflict) {
		t.Fatalf("空 URL err=%v", err)
	}
	if _, err := ws.Exec(ctx, "missing", "id"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Exec missing conn err=%v", err)
	}

	tr := mock.NewTraffic()
	if err := tr.StopCapture(ctx, "missing"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("StopCapture missing err=%v", err)
	}
	if _, err := tr.Replay(ctx, "missing", provider.Mutation{}); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Replay missing err=%v", err)
	}
}

func TestRegistry(t *testing.T) {
	r := provider.NewRegistry()
	if _, ok := r.C2(); ok {
		t.Fatal("空注册表不应有 C2")
	}
	if got := r.Bindings(); len(got) != 0 {
		t.Fatalf("空注册表 Bindings=%v", got)
	}

	var nilC2 provider.C2Provider
	if err := r.RegisterC2("c2", nilC2); !errors.Is(err, provider.ErrNilProvider) {
		t.Fatalf("nil provider err=%v", err)
	}
	c2 := mock.NewC2()
	if err := r.RegisterC2("c2", c2); err != nil {
		t.Fatalf("RegisterC2: %v", err)
	}
	if err := r.RegisterC2("c2", c2); !errors.Is(err, provider.ErrAlreadyRegistered) {
		t.Fatalf("重复注册 err=%v", err)
	}
	if got, ok := r.C2(); !ok || got != c2 {
		t.Fatalf("C2() 返回不对: %v %v", got, ok)
	}
	if err := r.RegisterWebShell("webshell", mock.NewWebShell()); err != nil {
		t.Fatalf("RegisterWebShell: %v", err)
	}
	if err := r.RegisterTraffic("traffic", mock.NewTraffic()); err != nil {
		t.Fatalf("RegisterTraffic: %v", err)
	}
	b := r.Bindings()
	if len(b) != 3 || b[provider.CapabilityC2] != "c2" || b[provider.CapabilityTraffic] != "traffic" {
		t.Fatalf("Bindings=%v", b)
	}
}
