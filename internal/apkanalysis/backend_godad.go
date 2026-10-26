package apkanalysis

// backend_godad.go — Go DAD 反编译后端:纯 Go 实现的 Basic 级 Java 反编译。
// 基于 androguard DAD 算法(无 SSA,reach-def + 区间分析),
// 支持构造器/字段/返回/简单调用/算术,复杂方法输出 /* op */ 注释。
// 依赖:零(纯 Go,无 Python/JVM 子进程)。

import (
	"context"
	"fmt"
	"strings"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/godad"
	"finduai/internal/apkengine/zipprobe"
)

// GoDadBackend 纯 Go 反编译后端(Basic 级 Java)。
type GoDadBackend struct{}

func NewGoDadBackend() *GoDadBackend { return &GoDadBackend{} }

func (b *GoDadBackend) Kind() string { return "go-dad" }
func (b *GoDadBackend) Name() string { return "Go-DAD (内置)" }

func (b *GoDadBackend) Health(_ context.Context) error {
	return nil // 零依赖,永远健康
}

// Decompile 给定 APK 路径 + dalvik 类名,反编译为 Java 源码。
func (b *GoDadBackend) Decompile(_ context.Context, samplePath, class string) (string, error) {
	dalvik := normalizeClassQuery(class, false)
	// 打开 APK,遍历 DEX 定义该类
	r, err := zipprobe.Open(samplePath)
	if err != nil {
		return "", fmt.Errorf("open apk: %w", err)
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		return "", fmt.Errorf("cd entries: %w", err)
	}
	zipprobe.SortByCompSize(entries)
	for _, e := range entries {
		data, err := zipprobe.InflateDex(r, e)
		if err != nil {
			continue
		}
		for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
			d, err := dex.Parse(ld.Buf, ld.Name)
			if err != nil {
				continue
			}
			cls := d.GetClassByName(dalvik)
			if cls == nil {
				continue
			}
			dec := godad.New(d)
			src := dec.DecompileClass(cls)
			if src == "" {
				return "", fmt.Errorf("decompilation produced no output for %s", dalvik)
			}
			return strings.TrimRight(src, "\n") + "\n", nil
		}
	}
	return "", fmt.Errorf("class %s not found in APK", dalvik)
}

func (b *GoDadBackend) Close() error { return nil }
