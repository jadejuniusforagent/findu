package findrefs

// CodeItemScanner — code_item 区间的引用索引扫描,移植自 code_item_scan.py。
// 原版以单字节 opcode 字符类正则 finditer 扫描,此处为等价的线性字节扫描
// (单字节匹配不存在重叠问题,位置语义一致)。

// opcode 字节类(对齐 code_item_scan.py 顶部的模板定义)。
var (
	stringIdxOps = [256]bool{0x1a: true, 0x1b: true} // const-string[/jumbo]
	typeIdxOps   = [256]bool{0x1c: true, 0x1f: true, 0x20: true, 0x22: true, 0x23: true, 0x24: true, 0x25: true}
	fieldIdxOps  = fieldRange()
	methodIdxOps = [256]bool{0x6e: true, 0x6f: true, 0x70: true, 0x71: true, 0x72: true,
		0x74: true, 0x75: true, 0x76: true, 0x77: true, 0x78: true, 0xfa: true, 0xfb: true}
)

func fieldRange() [256]bool {
	var t [256]bool
	for op := 0x52; op <= 0x6d; op++ {
		t[op] = true
	}
	return t
}

// scanFuncs 对齐 OPCODE_PATTERNS:各维度判定的 opcode 字节类。
var scanFuncs = map[string][256]bool{
	"string": stringIdxOps,
	"type":   typeIdxOps,
	"field":  fieldIdxOps,
	"method": methodIdxOps,
}

// DimResult 一个维度(string/type/method/field)的扫描产物。
// Offsets/Mids/Matched 三者与命中偏移一一对应;Mids 元素可能是多方法
// (R8 指令去重桶冲突)或 nil(verify 未归位)。
type DimResult struct {
	Offsets []int
	Mids    [][]uint32
	Matched []uint32
	Empty   bool // 维度无命中
}

type CodeItemScanner struct {
	insn     *InsnLocator
	offStart int
	offEnd   int
	buf      []byte
}

func NewCodeItemScanner(insn *InsnLocator) *CodeItemScanner {
	return &CodeItemScanner{
		insn:     insn,
		offStart: insn.CodeStart,
		offEnd:   insn.CodeEnd,
		buf:      insn.dex.Buf(),
	}
}

// scanCodeItem 对齐 _scan_code_item。命中偏移 = opcode 字节绝对位置。
// 越界即整体返回(原版 bugfix 语义,非 continue)。
func (s *CodeItemScanner) scanCodeItem(dim string, idxs map[uint32]struct{}) (DimResult, bool) {
	var res DimResult
	ops, ok := scanFuncs[dim]
	if !ok {
		return res, false
	}
	sub := s.buf[s.offStart:s.offEnd]
	bufLen := len(sub)
	for i := 0; i < bufLen; i++ {
		if !ops[sub[i]] {
			continue
		}
		idxOff := i + 2
		var idx uint32
		if dim == "string" && sub[i] == 0x1b { // const-string/jumbo
			if idxOff+4 > bufLen {
				return res, true
			}
			idx = u32at(sub, idxOff)
		} else {
			if idxOff+2 > bufLen {
				return res, true
			}
			idx = uint32(u16at(sub, idxOff))
		}
		if _, hit := idxs[idx]; !hit {
			continue
		}
		res.Offsets = append(res.Offsets, i+s.offStart)
		res.Mids = append(res.Mids, nil)
		res.Matched = append(res.Matched, idx)
	}
	return res, true
}

// Scan 对齐 scan():已定位索引 → 偏移扫描 → 指令归位方法。
// 入参为各维度的命中索引集合(空集视为该维度无命中)。
func (s *CodeItemScanner) Scan(scanned map[string]map[uint32]struct{}) map[string]*DimResult {
	out := make(map[string]*DimResult, len(scanned))
	for dim, idxs := range scanned {
		if len(idxs) == 0 {
			out[dim] = &DimResult{Empty: true}
			continue
		}
		res, ok := s.scanCodeItem(dim, idxs)
		if !ok {
			out[dim] = &DimResult{Empty: true}
			continue
		}
		res.Mids = s.insn.LocateOffset(res.Offsets)
		out[dim] = &res
	}
	return out
}
