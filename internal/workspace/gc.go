package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 平台运行数据目录（相对进程工作目录）：🔴 禁止进入任何回收集。
// 注册表层面它们本就不该登记；这里是 gc 的硬拒绝兜底——即使将来有人误登记，
// 真删也会在这一层被拦下。
var runtimeDataDirs = []string{DefaultWorkspaceRoot, "var/tmp/reduction"}

// Options GC 执行选项。
type Options struct {
	// RealDelete 真删开关。零值 false = dry-run（只出报告不动盘）——
	// 「默认 dry-run，配置显式开启才真删」直接编码在 API 形状里，
	// 调用方漏配时绝不可能误删。
	RealDelete bool
	// MaxDeleteBytes 单次真删的字节熔断上限；0 = 不限。候选总量超限时
	// 按「最久未访问优先」截断，剩余候选留待下一轮。
	MaxDeleteBytes int64
	// Audit 真删审计钩子（每条候选删除前回调：谁/哪个目录/多少字节/依据哪条策略）。
	// 🔴 RealDelete=true 时必须提供，否则拒绝执行——没有审计路径就没有真删。
	Audit func(line string)
	// Protector / AccessTracker 判定数据源；nil 的行为见 policy.go。
	Protector     Protector
	AccessTracker AccessTracker
}

// ErrAuditRequired 真删缺少审计钩子。
var ErrAuditRequired = errors.New("workspace: 真删必须显式提供审计钩子（Options.Audit），无审计不落盘")

// ── 最近一次 GC 结果（观测面消费，治理方案 §3.5）──

var (
	lastGCMu     sync.RWMutex
	lastGCReport *Report
)

// SetLastGCReport 记录最近一次 GC（含 dry-run）结果；RunGC 自动调用。
func SetLastGCReport(rep *Report) {
	if rep == nil {
		return
	}
	lastGCMu.Lock()
	lastGCReport = rep
	lastGCMu.Unlock()
}

// LastGCReport 返回最近一次 GC 结果（浅拷贝；无记录时 nil）。
func LastGCReport() *Report {
	lastGCMu.RLock()
	defer lastGCMu.RUnlock()
	if lastGCReport == nil {
		return nil
	}
	rep := *lastGCReport
	return &rep
}

// RunGC 按三层防线评估全部注册条目并按选项执行。
// 返回的报告 DryRun=false 时含实际删除结果；评估与删除中的单条失败记入
// Report.Errors，不中断整体。
func RunGC(ctx context.Context, opts Options) (*Report, error) {
	if opts.RealDelete && opts.Audit == nil {
		return nil, ErrAuditRequired
	}
	now := time.Now()
	rep := evaluate(ctx, Default.Entries(), opts.Protector, opts.AccessTracker, now, dirSize)
	SetLastGCReport(rep)
	if !opts.RealDelete {
		return rep, nil
	}

	// 真删：候选按最久未访问优先，套字节熔断，逐条审计后清空目录内容。
	SortCandidatesByAccess(rep.Candidates)
	rep.DryRun = false
	var deleted int64
	for _, c := range rep.Candidates {
		select {
		case <-ctx.Done():
			rep.Errors = append(rep.Errors, ctx.Err().Error())
			return rep, ctx.Err()
		default:
		}
		if opts.MaxDeleteBytes > 0 && deleted+c.Bytes > opts.MaxDeleteBytes {
			break
		}
		if err := purgeDir(c.Dir); err != nil {
			rep.Errors = append(rep.Errors, c.EntryName+": purge: "+err.Error())
			continue
		}
		deleted += c.Bytes
		rep.Deleted = append(rep.Deleted, c.Dir)
		opts.Audit(fmt.Sprintf("workspace-gc: entry=%s dir=%s bytes=%d reason=%s last_access=%s",
			c.EntryName, c.Dir, c.Bytes, c.Reason, c.LastAccess.Format(time.RFC3339)))
	}
	return rep, nil
}

// purgeDir 清空 dir 的全部内容但保留目录本身（各子系统启动时 MkdirAll，
// 期望根目录存在）。每个子路径过 PathWithin 双保险，且目录整体先过
// 运行数据硬拒绝。
func purgeDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := refuseRuntimeData(abs); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		child := filepath.Join(dir, e.Name())
		if !PathWithin(dir, child) {
			return fmt.Errorf("子路径越界，拒绝删除: %s", child)
		}
		if err := os.RemoveAll(child); err != nil {
			return err
		}
	}
	return nil
}

// refuseRuntimeData 词法判定 abs 是否落在平台运行数据目录内，是则拒绝。
func refuseRuntimeData(abs string) error {
	for _, rel := range runtimeDataDirs {
		root, err := filepath.Abs(rel)
		if err != nil {
			continue
		}
		if PathWithin(root, abs) {
			return fmt.Errorf("🔴 %s 属平台运行数据（%s），禁止进入回收集", abs, rel)
		}
	}
	return nil
}
