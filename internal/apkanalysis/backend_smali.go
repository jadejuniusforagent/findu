package apkanalysis

// backend_smali — smali 兜底后端：纯 Go 渲染类的 smali 字节码视图。
// 零外部依赖，永远可用（Health 恒为 nil），作为降级链的最终兜底。

import (
	"context"
	"fmt"
)

// SmaliBackend 输出类的 smali 风格字节码（纯 Go，反编译链未部署时可用）。
// render 由 service 注入（持有 DEX 缓存与 smali 渲染逻辑）。
type SmaliBackend struct {
	render func(samplePath, class string) (string, error)
}

// NewSmaliBackend 构建兜底后端。render 不可为 nil。
func NewSmaliBackend(render func(samplePath, class string) (string, error)) *SmaliBackend {
	return &SmaliBackend{render: render}
}

func (b *SmaliBackend) Kind() string { return "smali" }

func (b *SmaliBackend) Name() string { return "Smali (内置)" }

func (b *SmaliBackend) Health(context.Context) error {
	if b == nil || b.render == nil {
		return fmt.Errorf("smali renderer not wired")
	}
	return nil
}

func (b *SmaliBackend) Decompile(_ context.Context, samplePath, class string) (string, error) {
	if b == nil || b.render == nil {
		return "", fmt.Errorf("smali renderer not wired")
	}
	return b.render(samplePath, class)
}

func (b *SmaliBackend) Close() error { return nil }
