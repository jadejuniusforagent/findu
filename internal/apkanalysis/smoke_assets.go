package apkanalysis

import _ "embed"

// 冒烟样本（内嵌）：后端 --single-class / DAD 冒烟测试共用。
//
//go:embed testdata/smoke.apk
var smokeAPK []byte

// smokeClass 冒烟样本中的目标类（与 testdata/smoke.apk 内容对应）。
const smokeClass = "LByteStreamReader;"
