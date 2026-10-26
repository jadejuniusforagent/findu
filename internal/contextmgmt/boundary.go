package contextmgmt

import (
	"fmt"
	"strings"
)

// Boundary handling: folding records a compact_boundary note at the fold point instead of
// deleting the transcript, and lists what is still in effect so the model does not "forget"
// a constraint and re-ask for it or invent one from memory.
//
// The authority for those items lives outside the folded region (the leading system block and
// the blackboard blocks after the fold point), so the note only needs to point at them.

// ReinjectionWhitelist names what must keep applying after a fold. Empty fields are omitted.
type ReinjectionWhitelist struct {
	// TaskType is the current task stage (recon/web/post…); empty omits the line.
	TaskType string
	// RoleName is the active role name; empty omits the line.
	RoleName string
	// Extra appends caller-supplied pointer lines.
	Extra []string
}

// BoundaryNote renders the compact_boundary text (pure function). A non-positive droppedTokens
// omits the size estimate.
func BoundaryNote(droppedRounds, droppedTokens int, wl ReinjectionWhitelist) string {
	var b strings.Builder
	b.WriteString("## 历史折叠边界（compact_boundary，系统注入上下文，非用户输入）\n")
	if droppedRounds > 0 {
		if droppedTokens > 0 {
			fmt.Fprintf(&b, "已折叠最旧 %d 轮历史（约 %d tokens），原文可在会话记录 / 压缩与落盘副本中检索后再引用。\n", droppedRounds, droppedTokens)
		} else {
			fmt.Fprintf(&b, "已折叠最旧 %d 轮历史，原文可在会话记录 / 压缩与落盘副本中检索后再引用。\n", droppedRounds)
		}
	}
	b.WriteString("折叠**不影响**以下内容——它们仍在生效，勿因折叠遗忘、勿向用户重复索取：\n")
	b.WriteString("- 角色与操作约束：system 开头的角色提示词块（最高优先级）\n")
	b.WriteString("- 平台固定纪律：system 中的授权范围、写回规范、开工前查技能等指令\n")
	if wl.TaskType != "" || wl.RoleName != "" {
		line := "- 当前任务阶段"
		if wl.TaskType != "" {
			line += "：" + wl.TaskType
		}
		if wl.RoleName != "" {
			line += "（角色：" + wl.RoleName + "）"
		}
		b.WriteString(line + "\n")
	}
	for _, e := range wl.Extra {
		if e = strings.TrimSpace(e); e != "" {
			b.WriteString("- " + e + "\n")
		}
	}
	b.WriteString("- 项目黑板 / 事实与漏洞台账：折叠点之后的上下文块每轮重注\n")
	return strings.TrimRight(b.String(), "\n")
}
