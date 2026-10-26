package apkanalysis

// backend — 反编译后端抽象层。
// 设计原则：
//   - Backend 只负责"给定 APK 路径 + 类名，输出 Java/smali 源码"；
//     DEX 定位（locateClassDex）由 service 层完成，Backend 不感知。
//   - 注册到 BackendRegistry 后，GetSource 按降级链自动选择健康后端。
//   - Health 为 nil 表示后端可用；非 nil 表示不可用，降级到下一个。

import "context"

// DecompilerBackend 反编译后端接口。
type DecompilerBackend interface {
	// Kind 返回后端类型标识，用于日志、数据库、首选配置。
	// 取值："dad" | "jadx" | "go-dad" | "smali"
	Kind() string

	// Name 返回人类可读名，如 "DAD 4.1.2" / "jadx 1.5.0" / "Smali (内置)"。
	Name() string

	// Health 检查后端是否可用。返回 nil 表示可用。
	Health(ctx context.Context) error

	// Decompile 反编译指定类。
	// samplePath: APK 文件路径（已归档在工作区）。
	// class: dalvik 描述符形态，如 "Lcom/poc/Main;"。
	// 返回 Java 源码字符串；失败返回 error。
	Decompile(ctx context.Context, samplePath, class string) (string, error)

	// Close 释放后端资源（进程池、临时文件等）。可多次调用。
	Close() error
}
