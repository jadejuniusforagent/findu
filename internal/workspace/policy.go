package workspace

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"time"
)

// 回收候选的触发原因。
const (
	ReasonTTL   = "ttl"   // 无访问超时
	ReasonQuota = "quota" // 超容量水位
)

// Candidate 一条回收候选（dry-run 报告的最小单元）。粒度 = 注册目录本身；
// 目录内按文件细粒度挑选随 Protector/访问跟踪接入后再做。
type Candidate struct {
	EntryName  string    `json:"entry_name"`
	Dir        string    `json:"dir"`
	Bytes      int64     `json:"bytes"`
	Reason     string    `json:"reason"`
	LastAccess time.Time `json:"last_access"` // 零值 = 未知
}

// Report 一次策略评估 / GC 执行的结果。
type Report struct {
	At         time.Time    `json:"at"`
	DryRun     bool         `json:"dry_run"`
	Candidates []Candidate  `json:"candidates"`
	Skipped    []SkipRecord `json:"skipped"` // 被引用保护挡下的条目（审计可见）
	TotalBytes int64        `json:"total_bytes"`
	Deleted    []string     `json:"deleted,omitempty"` // 真删时实际清空的目录
	Errors     []string     `json:"errors,omitempty"`  // 解析/测容/删除失败（不中断整体）
}

// SkipRecord 引用保护命中记录（第一层防线的可见性）。
type SkipRecord struct {
	EntryName  string `json:"entry_name"`
	ProtectKey string `json:"protect_key"`
}

// Protector 引用保护判定：ProtectKey 命中 → 永不回收。由持有引用真相的
// 子系统实现（首个实现复用 buildsvc 的引用计数模型，见 service.go 的
// protectedSet/referenced/keepRef）。
type Protector interface {
	IsProtected(key string) bool
}

// AccessTracker 提供注册目录的最近访问时间（TTL 判定依据）。
// 返回零值 = 未知，该目录不参与 TTL 候选（不误删无访问数据的保守语义）。
type AccessTracker interface {
	LastAccess(dir string) time.Time
}

// WatermarkRatio 默认容量告警水位：占用达到 QuotaBytes 的该比例即开始回收。
const WatermarkRatio = 0.8

// Evaluate 对 entries 跑三层防线判定（引用保护 → TTL → 容量水位），
// 只产出候选与报告，不做任何删除。测试可通过 dirSize 替换测容。
func Evaluate(ctx context.Context, entries []Entry, prot Protector, tracker AccessTracker, now time.Time) *Report {
	return evaluate(ctx, entries, prot, tracker, now, dirSize)
}

func evaluate(ctx context.Context, entries []Entry, prot Protector, tracker AccessTracker, now time.Time, sizeFn func(string) (int64, error)) *Report {
	rep := &Report{At: now, DryRun: true}
	for _, e := range entries {
		select {
		case <-ctx.Done():
			rep.Errors = append(rep.Errors, ctx.Err().Error())
			return rep
		default:
		}
		// 第一层：引用保护优先，命中即跳过其余判定。
		if e.ProtectKey != "" && prot != nil && prot.IsProtected(e.ProtectKey) {
			rep.Skipped = append(rep.Skipped, SkipRecord{EntryName: e.Name, ProtectKey: e.ProtectKey})
			continue
		}
		dir, err := e.Base()
		if err != nil {
			rep.Errors = append(rep.Errors, e.Name+": "+err.Error())
			continue
		}
		var lastAccess time.Time
		if tracker != nil {
			lastAccess = tracker.LastAccess(dir)
		}
		// 第二层：TTL。访问时间未知（零值）时保守跳过。
		if e.TTL > 0 && !lastAccess.IsZero() && now.Sub(lastAccess) > e.TTL {
			size, serr := sizeFn(dir)
			if serr != nil {
				rep.Errors = append(rep.Errors, e.Name+": "+serr.Error())
				continue
			}
			rep.add(Candidate{EntryName: e.Name, Dir: dir, Bytes: size, Reason: ReasonTTL, LastAccess: lastAccess})
			continue
		}
		// 第三层：容量水位。
		if e.QuotaBytes > 0 {
			size, serr := sizeFn(dir)
			if serr != nil {
				rep.Errors = append(rep.Errors, e.Name+": "+serr.Error())
				continue
			}
			if float64(size) > float64(e.QuotaBytes)*WatermarkRatio {
				rep.add(Candidate{EntryName: e.Name, Dir: dir, Bytes: size, Reason: ReasonQuota, LastAccess: lastAccess})
			}
		}
	}
	return rep
}

func (r *Report) add(c Candidate) {
	r.Candidates = append(r.Candidates, c)
	r.TotalBytes += c.Bytes
}

// SortCandidatesByAccess 候选按最久未访问优先排序（容量回收顺序）；
// 访问时间未知（零值）排最前（最保守——最可能无人认领）。
func SortCandidatesByAccess(cands []Candidate) {
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i].LastAccess, cands[j].LastAccess
		if a.IsZero() != b.IsZero() {
			return a.IsZero()
		}
		return a.Before(b)
	})
}

// DirSize 目录实测占用（字节）的导出形态（观测端点用）；语义同包内 dirSize。
func DirSize(root string) (int64, error) { return dirSize(root) }

// dirSize 目录实测占用（字节）；目录不存在按 0 处理（尚无数据 ≠ 错误）。
func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}
