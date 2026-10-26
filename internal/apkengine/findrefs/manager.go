package findrefs

// Manager — 检索编排,对齐 findrefs_manager.py 的 find_ref 流程 +
// asc_handler.py 的结果行聚合。

import (
	"fmt"
	"sort"
	"strings"

	"finduai/internal/apkengine/dex"
)

type Manager struct {
	dex     *dex.DEX
	str     *StringLocator
	types   *TypeLocator
	methods *MemberLocator
	fields  *MemberLocator
	insn    *InsnLocator
	scanner *CodeItemScanner
}

func NewManager(d *dex.DEX) *Manager {
	str := NewStringLocator(d)
	types := NewTypeLocator(d, str)
	methods := NewMethodLocator(d, str, types)
	fields := NewFieldLocator(d, str, types)
	insn := NewInsnLocator(d)
	insn.Parse()
	return &Manager{
		dex:     d,
		str:     str,
		types:   types,
		methods: methods,
		fields:  fields,
		insn:    insn,
		scanner: NewCodeItemScanner(insn),
	}
}

// Query 一次检索请求;Method/Field 为 nil 表示不检索该维度。
type Query struct {
	String string
	Type   string
	Method *MemberFind
	Field  *MemberFind
}

// RefRow 聚合结果行,对齐 AscHandler.findrefs 输出行
// "dex | Lcls;->method | matched=(...)" 的结构化形态。
type RefRow struct {
	DEX     string   `json:"dex"`
	Class   string   `json:"class"`   // Lcom/poc/Main;
	Method  string   `json:"method"`  // ->方法名
	Matched []string `json:"matched"` // 命中项描述
	Line    string   `json:"line"`    // 原版行格式(差分对齐用)
}

// FindRef 对齐 find_ref:各维度先 locate 出索引集合,再经 code_item
// 扫描归位到方法。返回维度 → 命中明细。
func (m *Manager) FindRef(q *Query) map[string]*DimResult {
	scanned := map[string]map[uint32]struct{}{}
	hasIdx := false
	if q.String != "" {
		set := m.str.Locate(q.String)
		scanned["string"] = set
		hasIdx = hasIdx || len(set) > 0
	}
	if q.Type != "" {
		set := m.types.Locate(q.Type)
		scanned["type"] = set
		hasIdx = hasIdx || len(set) > 0
	}
	if q.Method != nil {
		set := m.methods.Locate(*q.Method)
		scanned["method"] = set
		hasIdx = hasIdx || len(set) > 0
	}
	if q.Field != nil {
		set := m.fields.Locate(*q.Field)
		scanned["field"] = set
		hasIdx = hasIdx || len(set) > 0
	}
	if !hasIdx {
		out := map[string]*DimResult{}
		for dim := range scanned {
			out[dim] = &DimResult{Empty: true}
		}
		return out
	}
	return m.scanner.Scan(scanned)
}

// formatMatchedName 对齐 _format_matched_name。
func (m *Manager) formatMatchedName(dim string, idx uint32) string {
	switch dim {
	case "string":
		return m.dex.GetString(idx)
	case "type":
		return m.dex.GetType(idx).Descriptor
	case "method":
		met := m.dex.GetMethod(idx)
		return fmt.Sprintf("%s->%s", met.Cls(), met.Name())
	case "field":
		f := m.dex.GetField(idx)
		return fmt.Sprintf("%s->%s", f.Cls(), f.Name())
	}
	return ""
}

// AggregateRows 对齐 AscHandler.findrefs(aggregate=True):
// 按方法聚合命中项;方法按索引升序,命中项按索引数值升序后格式化
// (与原版 sorted(grouped[mid]) 语义一致)。
func (m *Manager) AggregateRows(res map[string]*DimResult) []RefRow {
	type aggVal struct {
		seen  map[uint32]struct{}
		idxs  []uint32
		names []string
	}
	grouped := map[uint32]*aggVal{}
	var order []uint32

	for dim, dr := range res {
		if dr == nil || dr.Empty {
			continue
		}
		for i, mids := range dr.Mids {
			matched := m.formatMatchedName(dim, dr.Matched[i])
			for _, mid := range mids {
				v, ok := grouped[mid]
				if !ok {
					v = &aggVal{seen: map[uint32]struct{}{}}
					grouped[mid] = v
					order = append(order, mid)
				}
				if _, dup := v.seen[dr.Matched[i]]; !dup {
					v.seen[dr.Matched[i]] = struct{}{}
					v.idxs = append(v.idxs, dr.Matched[i])
					v.names = append(v.names, matched)
				}
			}
		}
	}

	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	rows := make([]RefRow, 0, len(order))
	for _, mid := range order {
		v := grouped[mid]
		idxs := append([]uint32(nil), v.idxs...)
		names := append([]string(nil), v.names...)
		sortIdxsByName := func(idxs []uint32, names []string) {
			for i := 0; i < len(idxs); i++ {
				for j := i + 1; j < len(idxs); j++ {
					if idxs[j] < idxs[i] {
						idxs[i], idxs[j] = idxs[j], idxs[i]
						names[i], names[j] = names[j], names[i]
					}
				}
			}
		}
		sortIdxsByName(idxs, names)
		met := m.dex.GetMethod(mid)
		row := RefRow{
			DEX:     m.dex.Name(),
			Class:   met.Cls(),
			Method:  met.Name(),
			Matched: names,
		}
		row.Line = fmt.Sprintf("%s | %s->%s | matched=(%s)",
			m.dex.Name(), met.Cls(), met.Name(), strings.Join(names, "; "))
		rows = append(rows, row)
	}
	return rows
}

// Dex 返回底层 DEX(字符串池遍历等只读消费;检索仍应走 FindRef)。
func (m *Manager) Dex() *dex.DEX { return m.dex }
