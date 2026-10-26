package zipprobe

// 通用 ZIP 条目读取(非 DEX):供 AndroidManifest.xml / resources.arsc 抽取。

import (
	"bytes"
	"fmt"
)

// ReadFile 按 name 从 APK 中定位并解压任一 ZIP 条目。
func ReadFile(r Reader, name string) ([]byte, error) {
	eocd, err := FindEOCD(r)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 8) // cd_size + cd_off;不能多读——EOCD 距文件尾仅 22 字节
	if _, err := r.ReadAt(head, eocd+12); err != nil {
		return nil, err
	}
	cdSize := int64(u32(head, 0))
	cdOff := int64(u32(head, 4))
	cd := make([]byte, cdSize)
	if _, err := r.ReadAt(cd, cdOff); err != nil {
		return nil, err
	}

	target := []byte(name)
	ptr := 0
	for ptr+46 <= len(cd) {
		if !bytes.Equal(cd[ptr:ptr+4], sigCentralDir) {
			break
		}
		nameLen := int(u16(cd, ptr+28))
		extraLen := int(u16(cd, ptr+30))
		commentLen := int(u16(cd, ptr+32))
		nameStart := ptr + 46
		nameEnd := nameStart + nameLen
		if nameEnd > len(cd) {
			break
		}
		if bytes.Equal(cd[nameStart:nameEnd], target) {
			entry := Entry{
				Name:       name,
				UncompSize: u32(cd, ptr+24),
				CompSize:   u32(cd, ptr+20),
				LocalOff:   u32(cd, ptr+42),
				Method:     u16(cd, ptr+10),
			}
			return InflateDex(r, entry)
		}
		ptr = nameEnd + extraLen + commentLen
	}
	return nil, fmt.Errorf("entry not found: %s", name)
}
