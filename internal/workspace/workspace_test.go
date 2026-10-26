package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testEntry(name, dir string, mutate func(*Entry)) Entry {
	e := Entry{
		Name:  name,
		Base:  func() (string, error) { return dir, nil },
		Scope: ScopeGlobal,
	}
	if mutate != nil {
		mutate(&e)
	}
	return e
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testEntry("evasion-artifacts", "data/evasion", nil)); err != nil {
		t.Fatalf("合法条目注册失败: %v", err)
	}
	if err := r.Register(testEntry("evasion-artifacts", "data/other", nil)); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("重复注册应报 ErrDuplicateEntry: %v", err)
	}
	if err := r.Register(Entry{Name: "no-base", Scope: ScopeGlobal}); err == nil {
		t.Fatal("Base 为 nil 应被拒绝")
	}
	if err := r.Register(testEntry("bad-scope", "x", func(e *Entry) { e.Scope = "weird" })); err == nil {
		t.Fatal("非法 Scope 应被拒绝")
	}
	if _, ok := r.Lookup("evasion-artifacts"); !ok {
		t.Fatal("已注册条目应可查到")
	}
	// Entries 稳定按 Name 排序（观测与 GC 遍历的确定性）。
	for _, n := range []string{"b-x", "a-x", "c-x"} {
		_ = r.Register(testEntry(n, "data/"+n, nil))
	}
	es := r.Entries()
	for i := 1; i < len(es); i++ {
		if es[i-1].Name > es[i].Name {
			t.Fatalf("Entries 未按 Name 排序: %s > %s", es[i-1].Name, es[i].Name)
		}
	}
}

func TestPolicyThreeLayers(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	quota := int64(1) << 30
	overQuota := quota * 9 / 10

	protector := stubProtector{"evasion:build:1": true}
	tracker := stubTracker{"old-dir": old}

	entries := []Entry{
		testEntry("protected", "data/protected", func(e *Entry) {
			e.ProtectKey = "evasion:build:1"
			e.TTL = time.Minute // 虽然访问早已超时，但引用保护优先
		}),
		testEntry("ttl-expired", "old-dir", func(e *Entry) { e.TTL = time.Hour }),
		testEntry("ttl-no-access-info", "unknown-dir", func(e *Entry) { e.TTL = time.Hour }),
		testEntry("over-quota", "big-dir", func(e *Entry) { e.QuotaBytes = 1 << 30 }),
		testEntry("under-quota", "small-dir", func(e *Entry) { e.QuotaBytes = 1 << 30 }),
	}
	sizes := map[string]int64{"old-dir": 10, "big-dir": quota * 9 / 10, "small-dir": 5}

	rep := evaluate(context.Background(), entries, protector, tracker, now,
		func(dir string) (int64, error) { return sizes[dir], nil })

	if got := len(rep.Skipped); got != 1 || rep.Skipped[0].EntryName != "protected" {
		t.Fatalf("引用保护应挡下 protected: %+v", rep.Skipped)
	}
	var ttlC, quotaC int
	for _, c := range rep.Candidates {
		switch c.Reason {
		case ReasonTTL:
			ttlC++
			if c.EntryName != "ttl-expired" {
				t.Fatalf("TTL 候选应为 ttl-expired: %+v", c)
			}
		case ReasonQuota:
			quotaC++
			if c.EntryName != "over-quota" {
				t.Fatalf("容量候选应为 over-quota（超 80%% 水位）: %+v", c)
			}
		}
	}
	if ttlC != 1 || quotaC != 1 {
		t.Fatalf("候选数不符: ttl=%d quota=%d", ttlC, quotaC)
	}
	if rep.TotalBytes != 10+overQuota {
		t.Fatalf("候选总量不符: %d", rep.TotalBytes)
	}
	if !rep.DryRun {
		t.Fatal("Evaluate 恒为 dry-run 语义")
	}
}

type stubProtector map[string]bool

func (s stubProtector) IsProtected(key string) bool { return s[key] }

type stubTracker map[string]time.Time

func (s stubTracker) LastAccess(dir string) time.Time { return s[dir] }

func TestRunGCDryRunByDefault(t *testing.T) {
	// Options 零值 = dry-run：有候选也不许动盘——真删必须显式 RealDelete。
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "victim.txt"), "dead")
	if err := Register(testEntry("gc-dry", dir, func(e *Entry) { e.TTL = time.Nanosecond })); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupEntry("gc-dry") })

	tracker := stubTracker{dir: time.Now().Add(-time.Hour)}
	rep, err := RunGC(context.Background(), Options{AccessTracker: tracker})
	if err != nil {
		t.Fatalf("dry-run 不应报错: %v", err)
	}
	if !rep.DryRun || len(rep.Candidates) != 1 || len(rep.Deleted) != 0 {
		t.Fatalf("dry-run 报告不符: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dir, "victim.txt")); err != nil {
		t.Fatal("dry-run 不得删除文件")
	}
}

func TestRunGCRealDeleteRequiresAuditAndPurges(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "a.txt"), "a")
	touch(t, filepath.Join(dir, "sub", "b.txt"), "b")
	if err := Register(testEntry("gc-real", dir, func(e *Entry) { e.TTL = time.Nanosecond })); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupEntry("gc-real") })
	tracker := stubTracker{dir: time.Now().Add(-time.Hour)}

	if _, err := RunGC(context.Background(), Options{RealDelete: true, AccessTracker: tracker}); !errors.Is(err, ErrAuditRequired) {
		t.Fatalf("真删无审计钩子应拒绝: %v", err)
	}

	var auditLines []string
	rep, err := RunGC(context.Background(), Options{
		RealDelete:    true,
		AccessTracker: tracker,
		Audit:         func(line string) { auditLines = append(auditLines, line) },
	})
	if err != nil {
		t.Fatalf("真删执行失败: %v", err)
	}
	if len(rep.Deleted) != 1 || len(auditLines) != 1 {
		t.Fatalf("删除与审计应一一对应: deleted=%v audit=%v", rep.Deleted, auditLines)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("目录内容应被清空")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatal("根目录本身应保留（子系统启动期 MkdirAll 的既有假设）")
	}
}

func TestRunGCRefusesRuntimeDataDirs(t *testing.T) {
	// 🔴 红线兜底：即使被误登记进注册表，var/tmp/workspace / var/tmp/reduction 真删也必须被拒。
	rel := filepath.Join(DefaultWorkspaceRoot, "conversations", "gc-refuse-test")
	touch(t, filepath.Join(rel, "keep"), "keep")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(rel)) }) // 仅删本测试创建的子目录

	if err := Register(testEntry("gc-runtime", rel, func(e *Entry) { e.TTL = time.Nanosecond })); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupEntry("gc-runtime") })

	rep, err := RunGC(context.Background(), Options{
		RealDelete:    true,
		AccessTracker: stubTracker{rel: time.Now().Add(-time.Hour)},
		Audit:         func(string) {},
	})
	if err != nil {
		t.Fatalf("整体执行不因单条拒绝而失败: %v", err)
	}
	if len(rep.Deleted) != 0 || len(rep.Errors) != 1 {
		t.Fatalf("运行数据目录应被硬拒绝且不产生删除: deleted=%v errors=%v", rep.Deleted, rep.Errors)
	}
	if _, err := os.Stat(filepath.Join(rel, "keep")); err != nil {
		t.Fatal("🔴 运行数据文件不得被删")
	}
}

func TestJoinBaseAndRegisterOrResolve(t *testing.T) {
	// JoinBase 与 filepath.Join 逐字同语义（含空 base 的相对回退）。
	if got, _ := JoinBase("", "x")(); got != "x" {
		t.Fatalf("空 base 应退化为相对路径: %q", got)
	}
	if got, _ := JoinBase("data", "a", "b")(); got != filepath.Join("data", "a", "b") {
		t.Fatalf("JoinBase 语义漂移: %q", got)
	}

	// RegisterOrResolve：upsert 语义——同名重入后写覆盖（与注册表前各子系统
	// 「用自己的 dataDir 拼装」行为一致，测试重复初始化安全）。
	cleanupEntry("test-jor")
	d1, err := RegisterOrResolve(testEntry("test-jor", "data/jor-one", nil))
	if err != nil {
		t.Fatalf("首次注册解析失败: %v", err)
	}
	d2, err := RegisterOrResolve(testEntry("test-jor", "data/jor-two", nil))
	if err != nil {
		t.Fatalf("重入解析失败: %v", err)
	}
	if d1 == d2 || d2 != filepath.Join("data", "jor-two") {
		t.Fatalf("重入应后写覆盖: d1=%q d2=%q", d1, d2)
	}
	if got, ok := Lookup("test-jor"); !ok || got.Name != "test-jor" {
		t.Fatalf("upsert 后条目应可见: %v %v", got, ok)
	}
	cleanupEntry("test-jor")

	// Resolve：未注册显式报错（不静默回退）。
	if _, err := Resolve("no-such-entry-xyz"); err == nil {
		t.Fatal("未注册条目应报错")
	}
}

func TestLastGCReportRecorded(t *testing.T) {
	dir := t.TempDir()
	if err := Register(testEntry("gc-last", dir, func(e *Entry) { e.TTL = time.Nanosecond })); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupEntry("gc-last") })
	if _, err := RunGC(context.Background(), Options{AccessTracker: stubTracker{dir: time.Now().Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	rep := LastGCReport()
	if rep == nil || len(rep.Candidates) != 1 {
		t.Fatalf("RunGC 后应记录最近一次报告（含候选）: %+v", rep)
	}
}

func TestGuardMovedSemantics(t *testing.T) {
	// 平移后语义抽查（既有全量断言仍留在 internal/security 的测试里，走薄转发）。
	root := t.TempDir()
	if err := WorkspaceCrossProjectGuard(root, filepath.Join(root, "projects", "other", "f"), "mine"); err == nil {
		t.Fatal("跨项目应拒绝")
	}
	if got := sanitizeWorkspaceSegment("../../etc"); got != "default" {
		t.Fatalf("非法段应退化为 default: %q", got)
	}
	if !PathWithin(root, root) || PathWithin(root, root+"-sibling") {
		t.Fatal("PathWithin 语义漂移")
	}
}

func touch(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cleanupEntry 从包级 Default 摘除测试登记项，避免跨用例泄漏。
func cleanupEntry(name string) {
	Default.mu.Lock()
	delete(Default.entries, name)
	Default.mu.Unlock()
}
