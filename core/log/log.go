// Package log 是 core 的统一日志入口。
//
// 第 1 批刻意的设计：内部委托给标准库 log，行格式、SetOutput 语义、与
// server/logsink.go 的 ring buffer/SSE 解析完全保持一致。这样搬运 28 个文件的
// 调用点时行为零变化，事后把内部换成 slog 时只需改这一处。
//
// 分层约束：属于 core，禁止 import domain/plugins/cmd。
package log

import (
	"io"
	stdlog "log"
)

// Print / Printf / Println 与标准库同名同义。
func Print(v ...any)                 { stdlog.Print(v...) }
func Printf(format string, v ...any) { stdlog.Printf(format, v...) }
func Println(v ...any)               { stdlog.Println(v...) }

// Fatal* 记录后以退出码 1 结束进程。
func Fatal(v ...any)                 { stdlog.Fatal(v...) }
func Fatalf(format string, v ...any) { stdlog.Fatalf(format, v...) }
func Fatalln(v ...any)               { stdlog.Fatalln(v...) }

// Panic* 记录后 panic。
func Panic(v ...any)                 { stdlog.Panic(v...) }
func Panicf(format string, v ...any) { stdlog.Panicf(format, v...) }

// SetOutput 重定向输出。server/logsink.go 通过它把日志接入 ring buffer 与 SSE。
func SetOutput(w io.Writer) { stdlog.SetOutput(w) }

// SetFlags / SetPrefix 保留标准库能力，供需要自定义行首的场景使用。
func SetFlags(flag int)       { stdlog.SetFlags(flag) }
func SetPrefix(prefix string) { stdlog.SetPrefix(prefix) }

// Writer 返回当前输出目标。
func Writer() io.Writer { return stdlog.Writer() }
