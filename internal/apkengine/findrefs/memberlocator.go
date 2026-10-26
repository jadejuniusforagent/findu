package findrefs

import (
	"strings"
	"sync"

	"finduai/internal/apkengine/dex"
)

// MemberFind 对齐 locate 入参结构 {"class": [clz, precise], "method"/"field": name}。
// Class 为 nil 表示不限定类;Name 为空表示取类下全部成员。
type MemberFind struct {
	Class      string
	ClassExact bool // true=精确类名(二分),false=fuzzy(经 TypeLocator)
	Name       string
}

// MemberLocator 定位 method_ids / field_ids,method_locator.py 与
// field_locator.py 结构完全同构,共用一份实现、由泛型方向参数区分。
type MemberLocator struct {
	dex       *dex.DEX
	str       *StringLocator
	types     *TypeLocator
	isMethod  bool
	buildOnce sync.Once
	clzMaps   map[uint32]map[uint32]struct{} // {type_idx : {member_idx}}
	nameMaps  map[uint32]map[uint32]struct{} // {name_idx : {member_idx}}
}

func NewMethodLocator(d *dex.DEX, str *StringLocator, types *TypeLocator) *MemberLocator {
	return &MemberLocator{dex: d, str: str, types: types, isMethod: true,
		clzMaps: map[uint32]map[uint32]struct{}{}, nameMaps: map[uint32]map[uint32]struct{}{}}
}

func NewFieldLocator(d *dex.DEX, str *StringLocator, types *TypeLocator) *MemberLocator {
	return &MemberLocator{dex: d, str: str, types: types, isMethod: false,
		clzMaps: map[uint32]map[uint32]struct{}{}, nameMaps: map[uint32]map[uint32]struct{}{}}
}

func (m *MemberLocator) BuildMap() {
	m.buildOnce.Do(func() {
		buf := m.dex.Buf()
		var table dex.IdTable
		if m.isMethod {
			table = m.dex.Header().Methods
		} else {
			table = m.dex.Header().Fields
		}
		off := int(table.Off)
		for idx := uint32(0); idx < table.Size; idx++ {
			classIdx := uint32(u16at(buf, off))
			nameIdx := u32at(buf, off+4)
			off += 8
			addToMapSet(m.clzMaps, classIdx, idx)
			addToMapSet(m.nameMaps, nameIdx, idx)
		}
	})
}

func addToMapSet(m map[uint32]map[uint32]struct{}, k, v uint32) {
	set, ok := m[k]
	if !ok {
		set = map[uint32]struct{}{}
		m[k] = set
	}
	set[v] = struct{}{}
}

// findTypeIdxPrecisely 对齐 _find_type_idx_precisely:type_ids 二分。
func (m *MemberLocator) findTypeIdxPrecisely(clz string) int {
	hdr := m.dex.Header()
	if hdr.Types.Size == 0 {
		return -1
	}
	left, right := int64(0), int64(hdr.Types.Size)-1
	for left <= right {
		mid := (left + right) >> 1
		descIdx := u32at(m.dex.Buf(), int(hdr.Types.Off)+int(mid)*4)
		desc := m.dex.GetString(descIdx)
		if desc == clz {
			return int(mid)
		}
		if desc < clz {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}
	return -1
}

func (m *MemberLocator) collectClzMids(typeIdxs map[uint32]struct{}) map[uint32]struct{} {
	ret := map[uint32]struct{}{}
	for typeIdx := range typeIdxs {
		for mid := range m.clzMaps[typeIdx] {
			ret[mid] = struct{}{}
		}
	}
	return ret
}

// matchClzMids 对齐 _match_clz_mids:成员名子串过滤。
func (m *MemberLocator) matchClzMids(mids map[uint32]struct{}, name string) map[uint32]struct{} {
	ret := map[uint32]struct{}{}
	for mid := range mids {
		var memberName string
		if m.isMethod {
			memberName = m.dex.GetMethod(mid).Name()
		} else {
			memberName = m.dex.GetField(mid).Name()
		}
		if strings.Contains(memberName, name) {
			ret[mid] = struct{}{}
		}
	}
	return ret
}

// Locate 对齐 locate 的四条路径:
//  1. 无类名:name 经 StringLocator 反查 name_idx → 成员
//  2. 精确类名 + 无方法名:类下全部成员
//  3. 精确类名 + 方法名:类成员子串过滤
//  4. fuzzy 类名:TypeLocator 集合 → 类成员集合,再与方法名索引交集
func (m *MemberLocator) Locate(find MemberFind) map[uint32]struct{} {
	m.BuildMap()

	clz := find.Class
	if clz == "" {
		clz = ""
	}
	method := find.Name
	clzPrecise := find.ClassExact

	if clz == "" && method == "" {
		return map[uint32]struct{}{}
	}

	if clz == "" {
		ret := map[uint32]struct{}{}
		for nameIdx := range m.str.Locate(method) {
			for mid := range m.nameMaps[nameIdx] {
				ret[mid] = struct{}{}
			}
		}
		return ret
	}

	var clzMids map[uint32]struct{}
	if clzPrecise {
		typeIdx := m.findTypeIdxPrecisely(clz)
		if typeIdx < 0 {
			return map[uint32]struct{}{}
		}
		set, ok := m.clzMaps[uint32(typeIdx)]
		if !ok {
			return map[uint32]struct{}{}
		}
		clzMids = make(map[uint32]struct{}, len(set))
		for k := range set {
			clzMids[k] = struct{}{}
		}
	} else {
		clzMids = m.collectClzMids(m.types.Locate(clz))
		if len(clzMids) == 0 {
			return map[uint32]struct{}{}
		}
	}
	if method == "" {
		return clzMids
	}
	if clzPrecise {
		return m.matchClzMids(clzMids, method)
	}
	ret := map[uint32]struct{}{}
	for nameIdx := range m.str.Locate(method) {
		for mid := range m.nameMaps[nameIdx] {
			if _, ok := clzMids[mid]; ok {
				ret[mid] = struct{}{}
			}
		}
	}
	return ret
}
