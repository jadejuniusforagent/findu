// Package fixture — 手工构造的最小合法 DEX/APK 测试夹具。
// 同一构造逻辑已复制到 ASC 仓库侧的差分脚本(testdata/gen_golden.py),
// 两边消费同一份提交进 testdata 的字节,保证差分基线一致。
package fixture

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
)

// testDEX — 手工构造的最小合法 DEX,覆盖 header/string/type/proto/field/
// method/class_def/class_data/code_item 全链路。也是 zipprobe/findrefs
// 差分测试的底座(同构造逻辑复制到 testdata/gen_fixture.py)。
type dexBuilder struct {
	strings []string
	types   []string // 描述符,最终按字节序排序
	protos  []protoDef
	fields  []fieldDef
	methods []methodDef
	classes []classDef
}

type protoDef struct {
	shorty string // 短描述符字符串(自动入 strings)
	ret    string // 返回类型描述符
	params []string
}

type fieldDef struct {
	class string
	typ   string
	name  string
}

type methodDef struct {
	class  string
	proto  protoDef
	name   string
	code   []byte // insns 原始字节(nil = 无方法体)
	direct bool
}

type classDef struct {
	class      string
	superclass string
	fields     []string // 字段名(需已在 fields 里注册)
	directs    []string // 方法名
	virtuals   []string
}

func (b *dexBuilder) strIdx(s string) uint32 {
	for i, v := range b.strings {
		if v == s {
			return uint32(i)
		}
	}
	b.strings = append(b.strings, s)
	return uint32(len(b.strings) - 1)
}

func (b *dexBuilder) typeIdx(desc string) uint32 {
	for _, t := range b.types {
		if t == desc {
			return uint32(sortIdx(b.types, desc))
		}
	}
	b.types = append(b.types, desc)
	return uint32(sortIdx(b.types, desc))
}

func sortIdx(list []string, v string) int {
	// 返回按字节序排序后的位置(构造期直接重排)
	for i, t := range list {
		if t == v {
			return i
		}
	}
	return -1
}

func uleb(n uint32) []byte {
	var out []byte
	if n == 0 {
		return []byte{0}
	}
	for n > 0x7f {
		out = append(out, byte(n&0x7f)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

func BuildTestDEX() []byte {
	b := &dexBuilder{}
	b.strIdx("Lcom/poc/Base;")
	protoVoid := protoDef{shorty: "V", ret: "V"}
	protoIZ := protoDef{shorty: "IZ", ret: "Z", params: []string{"I"}}
	// 指令引用关系(差分与单测的断言基线):
	// getCount: invoke-static main(0x71,method@1) + sget value(0x60,field@0)
	//   + const/4 + return
	// main: const-class Main(0x1C,type@2) + const-string secret_token
	//   (0x1A,0xEEEE 标记,布局期回填真实 string idx) + return-void
	b.methods = []methodDef{
		{class: "Lcom/poc/Main;", proto: protoIZ, name: "getCount", direct: false,
			code: []byte{0x71, 0x00, 0x01, 0x00, 0x00, 0x00, // invoke-static {v0}, method@1
				0x60, 0x00, 0x00, 0x00, // sget v0, field@0(value)
				0x12, 0x10, // const/4 v0, 1
				0x0F, 0x00}}, // return v0
		{class: "Lcom/poc/Main;", proto: protoVoid, name: "main", direct: true,
			code: []byte{0x1C, 0x00, 0x02, 0x00, // const-class v0, type@2(Main)
				0x1A, 0x00, 0xEE, 0xEE, // const-string v0, <secret_token 回填>
				0x0E, 0x00}}, // return-void
	}
	b.fields = []fieldDef{
		{class: "Lcom/poc/Main;", typ: "I", name: "value"},
		{class: "Lcom/poc/Main;", typ: "[I", name: "buffer"},
	}
	b.classes = []classDef{
		{class: "Lcom/poc/Main;", superclass: "Lcom/poc/Base;",
			fields: []string{"value", "buffer"}, directs: []string{"main"}, virtuals: []string{"getCount"}},
	}
	return b.build()
}

func (b *dexBuilder) build() []byte {
	// —— 第零遍:收集全部描述符到 type 表 ——
	collect := func(desc string) {
		for _, t := range b.types {
			if t == desc {
				return
			}
		}
		b.types = append(b.types, desc)
	}
	for _, p := range b.protos {
		collect(p.ret)
		for _, pt := range p.params {
			collect(pt)
		}
	}
	for _, f := range b.fields {
		collect(f.class)
		collect(f.typ)
	}
	for _, m := range b.methods {
		collect(m.class)
		collect(m.proto.ret)
		for _, pt := range m.proto.params {
			collect(pt)
		}
	}
	for _, c := range b.classes {
		collect(c.class)
		collect(c.superclass)
	}

	// —— 收集字符串(注册顺序)——
	// 占位首字符串:原版 locate() 返回 string_idx-1,string idx 0 永远
	// 不可检索;fixture 用哑串占住 idx 0,使断言可写(quirk 保留在引擎里)。
	b.strIdx("<fixture>")
	// 描述符本身也是字符串(type_ids 指向 string_ids)
	for _, t := range b.types {
		b.strIdx(t)
	}
	for _, m := range b.methods {
		b.strIdx(m.proto.shorty)
	}
	for _, m := range b.methods {
		b.strIdx(m.name)
	}
	for _, f := range b.fields {
		b.strIdx(f.name)
	}
	// 注意:原版怪癖——字符串表"最后一项"因 locate 依赖下一项偏移而不可
	// 检索;secret_token 会被 const-string 引用,故不放在最后。
	b.strIdx("secret_token")
	b.strIdx("hello world")

	sortedTypes := append([]string(nil), b.types...)
	sortStrings(sortedTypes)
	strIdxOf := func(s string) uint32 {
		for i, v := range b.strings {
			if v == s {
				return uint32(i)
			}
		}
		panic("missing string " + s)
	}
	typeIdxOf := func(desc string) uint32 {
		for i, t := range sortedTypes {
			if t == desc {
				return uint32(i)
			}
		}
		panic("missing type " + desc)
	}

	// —— proto 表:按方法注册序去重收集 ——
	protos := make([]protoDef, 0, len(b.methods))
	protoIdxOf := func(p protoDef) uint32 {
		for i, v := range protos {
			if v.shorty == p.shorty && v.ret == p.ret && len(v.params) == len(p.params) {
				same := true
				for i := range v.params {
					if v.params[i] != p.params[i] {
						same = false
						break
					}
				}
				if same {
					return uint32(i)
				}
			}
		}
		protos = append(protos, p)
		return uint32(len(protos) - 1)
	}
	methodProto := make([]uint32, len(b.methods))
	for i, m := range b.methods {
		methodProto[i] = protoIdxOf(m.proto)
	}

	// —— 布局:cur 从 headerSize 起算,所有落表偏移即绝对文件偏移(与真实 DEX 一致)——
	headerSize := uint32(0x70)
	cur := headerSize
	type section struct {
		off  uint32
		data []byte
	}
	var sections []*section
	place := func(data []byte) uint32 {
		off := cur
		cur += uint32(len(data))
		sections = append(sections, &section{off, data})
		return off
	}

	stringIdsSec := &section{data: make([]byte, len(b.strings)*4)}
	stringIdsOff := place(stringIdsSec.data)
	stringDataOffs := make([]uint32, len(b.strings))
	for i, s := range b.strings {
		item := uleb(uint32(len(s)))
		item = append(item, s...)
		item = append(item, 0x00)
		stringDataOffs[i] = place(item)
	}
	for i, off := range stringDataOffs {
		binary.LittleEndian.PutUint32(stringIdsSec.data[i*4:], off)
	}

	typeIdsSec := &section{data: make([]byte, len(sortedTypes)*4)}
	typeIdsOff := place(typeIdsSec.data)
	for i, t := range sortedTypes {
		binary.LittleEndian.PutUint32(typeIdsSec.data[i*4:], strIdxOf(t))
	}

	protoIdsSec := &section{data: make([]byte, len(protos)*12)}
	protoIdsOff := place(protoIdsSec.data)
	for i, p := range protos {
		paramOff := uint32(0)
		if len(p.params) > 0 {
			tmp := make([]byte, 4+len(p.params)*2)
			binary.LittleEndian.PutUint32(tmp, uint32(len(p.params)))
			for j, pt := range p.params {
				binary.LittleEndian.PutUint16(tmp[4+j*2:], uint16(typeIdxOf(pt)))
			}
			paramOff = place(tmp)
		}
		binary.LittleEndian.PutUint32(protoIdsSec.data[i*12:], strIdxOf(p.shorty))
		binary.LittleEndian.PutUint32(protoIdsSec.data[i*12+4:], typeIdxOf(p.ret))
		binary.LittleEndian.PutUint32(protoIdsSec.data[i*12+8:], paramOff)
	}

	fieldIdsSec := &section{data: make([]byte, len(b.fields)*8)}
	fieldIdsOff := place(fieldIdsSec.data)
	for i, f := range b.fields {
		binary.LittleEndian.PutUint16(fieldIdsSec.data[i*8:], uint16(typeIdxOf(f.class)))
		binary.LittleEndian.PutUint16(fieldIdsSec.data[i*8+2:], uint16(typeIdxOf(f.typ)))
		binary.LittleEndian.PutUint32(fieldIdsSec.data[i*8+4:], strIdxOf(f.name))
	}

	methodIdsSec := &section{data: make([]byte, len(b.methods)*8)}
	methodIdsOff := place(methodIdsSec.data)
	for i, m := range b.methods {
		binary.LittleEndian.PutUint16(methodIdsSec.data[i*8:], uint16(typeIdxOf(m.class)))
		binary.LittleEndian.PutUint16(methodIdsSec.data[i*8+2:], uint16(methodProto[i]))
		binary.LittleEndian.PutUint32(methodIdsSec.data[i*8+4:], strIdxOf(m.name))
	}

	// class_data + code_item(先放置),class_defs 占位随后回填
	classDefsSec := &section{data: make([]byte, len(b.classes)*32)}
	classDefsOff := place(classDefsSec.data)
	classDataOffs := make([]uint32, len(b.classes))
	for ci, c := range b.classes {
		directs := map[string]int{}
		virtuals := map[string]int{}
		for mi, m := range b.methods {
			if m.class != c.class {
				continue
			}
			if m.direct {
				directs[m.name] = mi
			} else {
				virtuals[m.name] = mi
			}
		}
		var cd []byte
		cd = append(cd, uleb(0)...)
		cd = append(cd, uleb(uint32(len(c.fields)))...)
		cd = append(cd, uleb(uint32(len(c.directs)))...)
		cd = append(cd, uleb(uint32(len(c.virtuals)))...)
		prev := uint32(0)
		for _, fn := range c.fields {
			for fi, f := range b.fields {
				if f.class == c.class && f.name == fn {
					cd = append(cd, uleb(uint32(fi)-prev)...)
					prev = uint32(fi)
					cd = append(cd, uleb(2)...)
					break
				}
			}
		}
		writeMethod := func(mi int, access byte) {
			m := b.methods[mi]
			cd = append(cd, uleb(uint32(mi)-prev)...)
			prev = uint32(mi)
			cd = append(cd, uleb(uint32(access))...)
			if m.code != nil {
				m.code = patchStringMarker(m.code, strIdxOf("secret_token"))
				hdr := make([]byte, 16)
				binary.LittleEndian.PutUint16(hdr, 0x08)     // registers_size
				binary.LittleEndian.PutUint16(hdr[4:], 0x01) // outs_size
				binary.LittleEndian.PutUint32(hdr[12:], uint32(len(m.code)/2))
				codeOff := place(append(hdr, m.code...))
				cd = append(cd, uleb(codeOff)...)
			} else {
				cd = append(cd, uleb(0)...)
			}
		}
		prev = 0
		for _, mn := range c.directs {
			writeMethod(directs[mn], 0x9)
		}
		prev = 0
		for _, mn := range c.virtuals {
			writeMethod(virtuals[mn], 0x1)
		}
		classDataOffs[ci] = place(cd)
	}
	for ci, c := range b.classes {
		off := ci * 32
		binary.LittleEndian.PutUint32(classDefsSec.data[off:], typeIdxOf(c.class))
		binary.LittleEndian.PutUint32(classDefsSec.data[off+4:], 0x1)
		binary.LittleEndian.PutUint32(classDefsSec.data[off+8:], typeIdxOf(c.superclass))
		binary.LittleEndian.PutUint32(classDefsSec.data[off+24:], classDataOffs[ci])
	}

	// —— 组装 ——
	fileSize := cur
	out := make([]byte, fileSize)
	for _, s := range sections {
		copy(out[s.off:], s.data)
	}
	copy(out, "dex\n035\x00")
	binary.LittleEndian.PutUint32(out[0x20:], fileSize)
	binary.LittleEndian.PutUint32(out[0x24:], headerSize)
	binary.LittleEndian.PutUint32(out[0x28:], 0x12345678)
	binary.LittleEndian.PutUint32(out[0x34:], 0) // map_off:检索路径不依赖 map_list
	binary.LittleEndian.PutUint32(out[0x38:], uint32(len(b.strings)))
	binary.LittleEndian.PutUint32(out[0x3C:], stringIdsOff)
	binary.LittleEndian.PutUint32(out[0x40:], uint32(len(sortedTypes)))
	binary.LittleEndian.PutUint32(out[0x44:], typeIdsOff)
	binary.LittleEndian.PutUint32(out[0x48:], uint32(len(protos)))
	binary.LittleEndian.PutUint32(out[0x4C:], protoIdsOff)
	binary.LittleEndian.PutUint32(out[0x50:], uint32(len(b.fields)))
	binary.LittleEndian.PutUint32(out[0x54:], fieldIdsOff)
	binary.LittleEndian.PutUint32(out[0x58:], uint32(len(b.methods)))
	binary.LittleEndian.PutUint32(out[0x5C:], methodIdsOff)
	binary.LittleEndian.PutUint32(out[0x60:], uint32(len(b.classes)))
	binary.LittleEndian.PutUint32(out[0x64:], classDefsOff)
	binary.LittleEndian.PutUint32(out[0x68:], fileSize)
	binary.LittleEndian.PutUint32(out[0x6C:], headerSize)

	return out
}

// patchStringMarker 把 code 中 16-bit 对齐的 0xEEEE 标记替换为给定 string idx。
func patchStringMarker(code []byte, strIdx uint32) []byte {
	out := make([]byte, len(code))
	copy(out, code)
	for i := 0; i+1 < len(out); i += 2 {
		if out[i] == 0xEE && out[i+1] == 0xEE {
			out[i] = byte(strIdx)
			out[i+1] = byte(strIdx >> 8)
		}
	}
	return out
}

func sortStrings(list []string) {
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j] < list[i] {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
}

// BuildTestAPK 构造含 classes.dex 与 classes2.dex(均为 deflate)的 ZIP。
// dex2 是 dex1 的字节级副本改名,足以驱动多 DEX 检索路径。
func BuildTestAPK() []byte {
	dex := BuildTestDEX()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"classes.dex", "classes2.dex"} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			panic(err)
		}
		if _, err := w.Write(dex); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// BuildInvokeCustomDEX 构造含 invoke-custom 指令的最小 DEX。
// 用于 mini-DEX 抽取的降级路径测试(call_site 引用不支持,须报错)。
// 与 BuildTestDEX 的差分基线无关:独立函数,不改既有字节。
func BuildInvokeCustomDEX() []byte {
	b := &dexBuilder{}
	b.strIdx("Lcom/poc/Base;")
	protoVoid := protoDef{shorty: "V", ret: "V"}
	b.methods = []methodDef{
		{class: "Lcom/poc/Lambda;", proto: protoVoid, name: "callSite", direct: true,
			code: []byte{0xfc, 0x00, 0x00, 0x00, // invoke-custom {}, callsite@0
				0x0e, 0x00}}, // return-void
	}
	b.classes = []classDef{
		{class: "Lcom/poc/Lambda;", superclass: "Lcom/poc/Base;",
			directs: []string{"callSite"}},
	}
	return b.build()
}
