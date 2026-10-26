package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"finduai/core/log"
	"finduai/domain/conversation"
	"finduai/domain/exploration"
	"finduai/domain/finding"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"finduai/agent"
	"finduai/intercept"
	actool "finduai/tools"
)

func (s *Server) listActiveFindingRetests(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	items, err := pg.ListActiveFindingRetests(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"retests": items})
}

func (s *Server) listFindingRetests(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad finding id")
		return
	}
	f, err := pg.GetFinding(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if f == nil {
		writeErr(w, 404, "finding not found")
		return
	}
	items, err := pg.ListFindingRetests(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"retests": items})
}

func (s *Server) startFindingRetest(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad finding id")
		return
	}
	var req struct {
		Notes string `json:"notes"`
	}
	if !decodeConversationRequest(w, r, &req) {
		return
	}
	req.Notes = strings.TrimSpace(req.Notes)
	if utf8.RuneCountInString(req.Notes) > 4000 {
		writeErr(w, 400, "复测补充说明最多 4000 个字符")
		return
	}
	f, err := pg.GetFinding(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if f == nil {
		writeErr(w, 404, "finding not found")
		return
	}
	a, err := pg.GetAgentByKey(finding.FindingRetestAgentKey)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if a == nil || !a.Enabled {
		writeErr(w, 409, "漏洞复测 Agent 不存在或未启用，请在 Agent 管理中配置 retester")
		return
	}
	for _, key := range []string{"get_finding_retest_context", "record_finding_retest_result"} {
		t, err := pg.GetTool(key)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if t == nil || !t.Enabled || !slices.Contains(t.Agents, a.Key) {
			writeErr(w, 409, "请为复测 Agent 启用并绑定工具："+key)
			return
		}
	}
	if s.resolveChatAgent(&conversation.Conversation{AgentKey: a.Key}) == nil {
		writeErr(w, 503, s.chatUnavailableReason())
		return
	}
	if s.ctx.Err() != nil {
		writeErr(w, 503, "服务正在停止")
		return
	}
	retest, conv, created, err := pg.CreateFindingRetest(r.Context(), id, req.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "finding not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if created {
		busyKey := s.convBusyKey(conv.ID)
		s.chatMu.Lock()
		s.chatBusy[busyKey] = true
		s.chatMu.Unlock()
		s.runConversation(conv, retest.InitialMessage(), busyKey)
	}
	code := http.StatusOK
	if created {
		code = http.StatusAccepted
	}
	writeJSON(w, code, map[string]any{"retest": retest, "created": created})
}

// Both tools derive the finding from server-owned conversation context. Tool
// arguments cannot redirect a result into a different finding or conversation.
func (s *Server) findingRetestTools() []actool.CoreTool {
	return []actool.CoreTool{
		roTool("get_finding_retest_context", "读取当前复测会话关联的漏洞证据快照、复测状态、补充说明与当前任务约束。无参数，只能读取本会话。",
			objSchema(map[string]any{}), func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
				r, err := s.m.pg.FindingRetestForConversation(ctx, intercept.ConvIDFromContext(ctx))
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if r == nil {
					return actool.Errorf("当前会话未关联复测记录，请从漏洞详情发起复测"), nil
				}
				var constraints []exploration.Constraint
				f, err := s.m.pg.GetFinding(r.FindingID)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if f != nil && f.TaskID != nil {
					if task, ok := s.m.Task(strconv.FormatInt(*f.TaskID, 10)); ok {
						constraints, err = task.Store.ListConstraints()
						if err != nil {
							return actool.Errorf(err.Error()), nil
						}
					}
				}
				return jsonResult(map[string]any{"retest": r, "current_constraints": constraints})
			}),
		wrTool("record_finding_retest_result", "为当前复测会话保存唯一结论；原漏洞证据与报告保持不变。会话成功结束且结论为 fixed 时，系统自动将漏洞状态改为已修复；其他结论保留原状态。必须提供本次实际检查的证据，无法确认时写明阻塞原因。",
			objSchema(map[string]any{
				"verdict":  map[string]any{"type": "string", "enum": []string{"reproduced", "fixed", "inconclusive"}},
				"summary":  strParam("本次复测结论摘要"),
				"evidence": strParam("Markdown：本次实际步骤、观察、对照、结论依据；无法确认则列出已检查内容和阻塞原因"),
			}, "verdict", "summary", "evidence"), func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
				var a struct {
					Verdict  string `json:"verdict"`
					Summary  string `json:"summary"`
					Evidence string `json:"evidence"`
				}
				if err := json.Unmarshal(in, &a); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if err := s.m.pg.RecordFindingRetestResult(ctx, intercept.ConvIDFromContext(ctx), a.Verdict, a.Summary, a.Evidence); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return jsonResult(map[string]any{"saved": true, "verdict": a.Verdict})
			}),
	}
}

// Seed the editable agent atomically, without an automatic discovery trigger.
// Once seeded, user edits/deletion survive restarts; a pre-existing key is kept.
func (s *Server) seedFindingRetester() error {
	for _, t := range s.findingRetestTools() {
		schema, _ := json.Marshal(t.InputSchema())
		bindings, _ := json.Marshal([]string{finding.FindingRetestAgentKey})
		if err := s.m.pg.SeedTool(t.Name(), t.Description(), schema, bindings); err != nil {
			return err
		}
	}
	const flag = "finding_retester_seed_v1"
	_, err := s.m.pg.SeedBuiltinAgent(finding.FindingRetestAgentKey, "漏洞复测", "从漏洞详情手动启动，读取原证据并保存独立复测结论。", "assistant", agent.RetesterDefaultPrompt, flag)
	return err
}

func (s *Server) finishRetest(id int64, status, reason string) {
	if err := s.m.pg.FinishFindingRetest(id, status, reason); err != nil {
		// Surface failure to the conversation runner rather than inventing a result.
		log.Printf("[retest %d] finish: %v", id, err)
	}
}
