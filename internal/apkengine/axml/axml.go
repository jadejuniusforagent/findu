// Package axml — Android 二进制 XML(AXML)解码,自包含实现。
// ASC 原版将 manifest 解码委托给 androguard;Go 侧以最小 AXML 解析器
// 等价输出(manifest 查看所需的标签/属性/权限清单)。
package axml

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	resXMLType        = 0x0003
	resStringPoolType = 0x0001
	resXMLStartNSType = 0x0100
	resXMLStartElType = 0x0102
)

// Attribute 解析后的属性。
type Attribute struct {
	Name     string
	Value    string
	RawValue string
	DataType uint8
	Data     int32
}

// Element 文档树节点。
type Element struct {
	Name       string
	Attributes []Attribute
	Children   []*Element
	Text       string
}

func (e *Element) Attr(name string) (string, bool) {
	for _, a := range e.Attributes {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// FindAll 深度优先收集指定标签名的全部节点。
func (e *Element) FindAll(name string) []*Element {
	var out []*Element
	var walk func(*Element)
	walk = func(n *Element) {
		if n.Name == name {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, c := range e.Children {
		walk(c)
	}
	return out
}

// Decode 解析 AXML 字节,返回根元素(manifest)。
func Decode(data []byte) (*Element, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("axml: too short")
	}
	if binary.LittleEndian.Uint16(data) != resXMLType {
		return nil, fmt.Errorf("axml: bad chunk type %#x", binary.LittleEndian.Uint16(data))
	}
	strings_, err := parseStringPool(data)
	if err != nil {
		return nil, err
	}

	var stack []*Element
	var root *Element
	pos := 8
	for pos+8 <= len(data) {
		chunkType := binary.LittleEndian.Uint16(data[pos:])
		headerSize := int(binary.LittleEndian.Uint16(data[pos+2:]))
		chunkSize := int(binary.LittleEndian.Uint32(data[pos+4:]))
		if chunkSize <= 0 || pos+chunkSize > len(data) {
			break
		}
		body := pos + headerSize

		switch chunkType {
		case resXMLStartElType:
			if body+20 > pos+chunkSize {
				break
			}
			nameIdx := binary.LittleEndian.Uint32(data[body+4:])
			name := poolGet(strings_, nameIdx)
			attrStart := int(binary.LittleEndian.Uint16(data[body+8:]))
			attrSize := int(binary.LittleEndian.Uint16(data[body+10:]))
			attrCount := int(binary.LittleEndian.Uint16(data[body+12:]))
			el := &Element{Name: name}
			if attrSize >= 20 {
				for i := 0; i < attrCount; i++ {
					off := body + attrStart + i*attrSize
					if off+20 > len(data) {
						break
					}
					attrName := poolGet(strings_, binary.LittleEndian.Uint32(data[off+4:]))
					rawIdx := binary.LittleEndian.Uint32(data[off+8:])
					dataType := data[off+15]
					dataVal := int32(binary.LittleEndian.Uint32(data[off+16:]))
					attr := Attribute{
						Name:     attrName,
						DataType: dataType,
						Data:     dataVal,
						Value:    formatValue(dataType, dataVal, strings_, rawIdx),
					}
					if rawIdx != 0xFFFFFFFF {
						attr.RawValue = poolGet(strings_, rawIdx)
						attr.Value = attr.RawValue
					}
					el.Attributes = append(el.Attributes, attr)
				}
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, el)
			} else if root == nil {
				root = el
			}
			stack = append(stack, el)
		case 0x0103: // end element
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
		pos += chunkSize
	}
	if root == nil {
		return nil, fmt.Errorf("axml: no root element")
	}
	return root, nil
}

func parseStringPool(data []byte) ([]string, error) {
	for pos := 8; pos+8 <= len(data); {
		chunkType := binary.LittleEndian.Uint16(data[pos:])
		headerSize := int(binary.LittleEndian.Uint16(data[pos+2:]))
		chunkSize := int(binary.LittleEndian.Uint32(data[pos+4:]))
		if chunkSize <= 0 || pos+chunkSize > len(data) {
			return nil, fmt.Errorf("axml: bad chunk at %d", pos)
		}
		if chunkType == resStringPoolType {
			if pos+28 > len(data) {
				return nil, fmt.Errorf("axml: string pool header truncated")
			}
			stringCount := int(binary.LittleEndian.Uint32(data[pos+8:]))
			flags := binary.LittleEndian.Uint32(data[pos+16:])
			stringsStart := int(binary.LittleEndian.Uint32(data[pos+20:]))
			isUTF8 := flags&0x100 != 0
			out := make([]string, 0, stringCount)
			offsets := pos + headerSize
			for i := 0; i < stringCount; i++ {
				if offsets+4*i+4 > len(data) {
					break
				}
				off := pos + stringsStart + int(binary.LittleEndian.Uint32(data[offsets+4*i:]))
				if off >= len(data) {
					continue
				}
				if isUTF8 {
					s, _ := readUTF8String(data, off)
					out = append(out, s)
				} else {
					s, _ := readUTF16String(data, off)
					out = append(out, s)
				}
			}
			return out, nil
		}
		pos += chunkSize
	}
	return nil, fmt.Errorf("axml: string pool not found")
}

func poolGet(pool []string, idx uint32) string {
	if int(idx) < len(pool) {
		return pool[idx]
	}
	return ""
}

func readUTF16String(data []byte, off int) (string, int) {
	chars, n := binary.LittleEndian.Uint16(data[off:]), 2
	if chars&0x8000 != 0 { // 高字扩展
		chars = ((uint16(data[off]) & 0x7f) << 8) | uint16(data[off+1])
	}
	units := make([]uint16, 0, chars)
	for i := 0; i < int(chars); i++ {
		p := off + n + i*2
		if p+2 > len(data) {
			break
		}
		units = append(units, binary.LittleEndian.Uint16(data[p:]))
	}
	return strings.TrimRight(string(utf16.Decode(units)), "\x00"), 0
}

func readUTF8String(data []byte, off int) (string, int) {
	// u16len(可变长) + u8len(可变长) + 字节
	skipVarint := func(p int) int {
		if data[p]&0x80 != 0 {
			return p + 2
		}
		return p + 1
	}
	p := skipVarint(off)
	u8len := int(data[p])
	if data[p]&0x80 != 0 {
		u8len = ((int(data[p]) & 0x7f) << 8) | int(data[p+1])
	}
	p = skipVarint(p)
	end := p + u8len
	if end > len(data) {
		end = len(data)
	}
	return string(data[p:end]), 0
}

func formatValue(dataType uint8, data int32, pool []string, rawIdx uint32) string {
	switch dataType {
	case 0x03: // STRING
		return poolGet(pool, uint32(data))
	case 0x10, 0x11: // INT_DEC / INT_HEX
		return fmt.Sprintf("%d", data)
	case 0x12: // BOOLEAN
		if data != 0 {
			return "true"
		}
		return "false"
	case 0x01: // REFERENCE
		return fmt.Sprintf("@%#x", uint32(data))
	default:
		return fmt.Sprintf("%#x", uint32(data))
	}
}

var _ = utf8.RuneLen

// XML 序列化(缩进两空格),供 manifest 查看器直出。
func (e *Element) XML() string {
	var sb strings.Builder
	writeElement(&sb, e, 0)
	return sb.String()
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func writeElement(sb *strings.Builder, e *Element, depth int) {
	indent := strings.Repeat("  ", depth)
	sb.WriteString(indent + "<" + e.Name)
	for _, a := range e.Attributes {
		sb.WriteString(" " + a.Name + `="` + escapeXML(a.Value) + `"`)
	}
	if len(e.Children) == 0 && e.Text == "" {
		sb.WriteString(" />\n")
		return
	}
	sb.WriteString(">")
	if e.Text != "" {
		sb.WriteString(escapeXML(e.Text))
	}
	if len(e.Children) > 0 {
		sb.WriteString("\n")
		for _, c := range e.Children {
			writeElement(sb, c, depth+1)
		}
		sb.WriteString(indent)
	}
	sb.WriteString("</" + e.Name + ">\n")
}
