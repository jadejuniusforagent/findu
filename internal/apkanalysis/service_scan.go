package apkanalysis

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/findrefs"
	"finduai/internal/apkengine/zipprobe"
)

// ────────────────────────── 检索 ──────────────────────────

type FindRefsRequest struct {
	Sample   string `json:"apk"`       // 样本 id 或文件名
	FindType string `json:"find_type"` // string / type / method / field
	Value    string `json:"value"`     // 查询值(string/type 直接匹配;method/field 为成员名)
	Class    string `json:"class"`     // method/field 时的类名过滤
	Fuzzy    bool   `json:"fuzzy"`     // 类名模糊开关
	Limit    int    `json:"limit"`     // 默认 1000,硬上限 5000
}

type FindRefsResult struct {
	SampleID   string            `json:"sample_id"`
	SampleName string            `json:"sample_name"`
	FindType   string            `json:"find_type"`
	Value      string            `json:"value"`
	Class      string            `json:"class,omitempty"`
	TotalHits  int               `json:"total_hits"`
	Limited    bool              `json:"limited"`
	Limit      int               `json:"limit"`
	DurationMS int64             `json:"duration_ms"`
	Warnings   []string          `json:"warnings,omitempty"` // 单个 DEX 损坏等非致命问题(检索继续)
	Rows       []findrefs.RefRow `json:"rows"`
}

// FindRefs 执行一次交叉引用检索(同步,ASC 引擎为毫秒~秒级)。
func (s *Service) FindRefs(ctx context.Context, req FindRefsRequest) (*FindRefsResult, error) {
	start := time.Now()
	sample, err := s.GetSample(req.Sample)
	if err != nil {
		return nil, err
	}
	// 容错归一:Agent 常按正则习惯传 "a|b|c" 多模式与点号类型名 —— 拆分为
	// 多次引擎检索合并(type 查询同步做 点号→斜杠 归一化),避免字面量必然落空。
	queries, findType, err := expandFindQuery(req)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultRows
	}
	if limit > MaxRows {
		limit = MaxRows
	}

	result := &FindRefsResult{
		SampleID:   sample.ID,
		SampleName: sample.Name,
		FindType:   findType,
		Value:      req.Value,
		Class:      req.Class,
		Limit:      limit,
	}

	err = forEachDex(ctx, s.log, sample.Workspace, &result.Warnings, func(mgr *findrefs.Manager) {
		if result.TotalHits >= limit {
			return
		}
		for _, find := range queries {
			if result.TotalHits >= limit {
				break
			}
			res := mgr.FindRef(find)
			for _, row := range mgr.AggregateRows(res) {
				result.TotalHits += len(row.Matched)
				if len(result.Rows) < limit {
					result.Rows = append(result.Rows, row)
				}
			}
		}
	})
	result.DurationMS = time.Since(start).Milliseconds()
	result.Limited = result.TotalHits > len(result.Rows)

	status, errText := "done", ""
	if err != nil {
		status, errText = "error", err.Error()
	}
	// 落库摘要:行数截断,避免 5000 行结果整份写进任务表
	recordResult := *result
	if len(recordResult.Rows) > maxRecordRows {
		recordResult.Rows = recordResult.Rows[:maxRecordRows]
	}
	s.recordTask(sample, "findrefs", req, &recordResult, status, errText, result.DurationMS)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// expandFindQuery 把一次请求展开为 1..N 个引擎查询:
//   - string/type 的 value 含 "|" 时拆分为多模式(Lucene/正则习惯容错)
//   - type 查询做 点号→斜杠 归一化(com.xiaopeng → com/xiaopeng,已含 / 则不动)
func expandFindQuery(req FindRefsRequest) ([]*findrefs.Query, string, error) {
	findType := strings.TrimSuffix(req.FindType, " refs") // 兼容 "string refs" 形态
	splitAlt := func(v string) []string {
		parts := strings.Split(v, "|")
		out := parts[:0]
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return []string{v}
		}
		return out
	}
	switch findType {
	case "string":
		if req.Value == "" {
			return nil, "", fmt.Errorf("string search needs value")
		}
		qs := make([]*findrefs.Query, 0)
		for _, v := range splitAlt(req.Value) {
			qs = append(qs, &findrefs.Query{String: v})
		}
		return qs, "string", nil
	case "type":
		if req.Value == "" {
			return nil, "", fmt.Errorf("type search needs value")
		}
		qs := make([]*findrefs.Query, 0)
		for _, v := range splitAlt(req.Value) {
			qs = append(qs, &findrefs.Query{Type: normalizeTypeQuery(v)})
		}
		return qs, "type", nil
	case "method", "field":
		class := normalizeClassQuery(req.Class, req.Fuzzy)
		if class == "" && req.Value == "" {
			return nil, "", fmt.Errorf("%s search needs class or name", findType)
		}
		mf := &findrefs.MemberFind{Class: class, ClassExact: !req.Fuzzy, Name: req.Value}
		if findType == "method" {
			return []*findrefs.Query{{Method: mf}}, "method", nil
		}
		return []*findrefs.Query{{Field: mf}}, "field", nil
	}
	return nil, "", fmt.Errorf("unsupported find_type: %s", req.FindType)
}

// normalizeTypeQuery type 检索值归一:点号转斜杠(com.a.B → com/a/B);
// 已是斜杠/描述符形态(Lcom/a/B;)原样返回。
func normalizeTypeQuery(v string) string {
	if strings.Contains(v, "/") {
		return v
	}
	return strings.ReplaceAll(v, ".", "/")
}

// forEachDex 打开 APK,按 comp_size 顺序遍历全部逻辑 DEX。单个 DEX inflate/parse
// 失败仅记警告继续(warnings 非 nil 时追加,封顶 8 条),整体检索不被畸形条目终止;
// 仅 APK 打不开/中央目录损坏时返回 error。warnings 传 nil 则只记日志。
func forEachDex(ctx context.Context, log *zap.Logger, apkPath string, warnings *[]string, fn func(mgr *findrefs.Manager)) error {
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return err
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		return err
	}
	zipprobe.SortByCompSize(entries)
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		data, err := zipprobe.InflateDex(r, e)
		if err != nil {
			warnDex(log, warnings, e.Name+": "+err.Error())
			continue
		}
		for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
			d, err := dex.Parse(ld.Buf, ld.Name)
			if err != nil {
				warnDex(log, warnings, ld.Name+": "+err.Error())
				continue
			}
			fn(findrefs.NewManager(d))
		}
	}
	return nil
}

func warnDex(log *zap.Logger, warnings *[]string, msg string) {
	log.Warn("android dex walk skip", zap.String("reason", msg))
	if warnings != nil && len(*warnings) < 8 {
		*warnings = append(*warnings, msg)
	}
}

// normalizeClassQuery 移植 _normalize_class_query:fuzzy 时点号转斜杠;
// 精确时补 L 前缀与分号(dalvik 描述符形态)。
func normalizeClassQuery(name string, fuzzy bool) string {
	if name == "" {
		return ""
	}
	if fuzzy {
		if strings.Contains(name, ".") && !strings.Contains(name, "/") {
			return strings.ReplaceAll(name, ".", "/")
		}
		return name
	}
	if strings.HasPrefix(name, "L") && strings.HasSuffix(name, ";") && strings.Contains(name, "/") {
		return name
	}
	name = strings.ReplaceAll(name, ".", "/")
	if !strings.HasPrefix(name, "L") {
		name = "L" + name
	}
	if !strings.HasSuffix(name, ";") {
		name += ";"
	}
	return name
}
