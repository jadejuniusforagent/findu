package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finduai/agent"
	"finduai/db"
	"finduai/transcript"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

func TestBridgeAttachmentAndRoleMapping(t *testing.T) {
	msg := composeBridgeMessage("看图", []finduAttachment{{FileName: "a.png", ServerPath: "/app/chat_uploads/a.png"}})
	if !strings.Contains(msg, "/app/chat_uploads/a.png") || !strings.Contains(msg, "a.png") {
		t.Fatalf("composed = %q", msg)
	}
	cases := map[string]string{
		"":        "findu_default",
		"默认":      "findu_default",
		"渗透测试":    "pentest",
		"报告撰写":    "reporter",
		"漏洞复测":    "retester",
		"审计取证":    "findu_audit",
		"unknown": "findu_default",
	}
	for role, want := range cases {
		if got := finduRoleAgentKey(role); got != want {
			t.Fatalf("role %q => %q, want %q", role, got, want)
		}
	}
}

func TestFinduGetConversation(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	if _, err := pg.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id text PRIMARY KEY, conversation_id text NOT NULL, role text NOT NULL,
			content text NOT NULL DEFAULT '', mcp_execution_ids text,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
			reasoning_content text
		);
		CREATE TABLE IF NOT EXISTS process_details (
			id text PRIMARY KEY, message_id text NOT NULL, conversation_id text NOT NULL,
			event_type text NOT NULL, message text NOT NULL DEFAULT '', data text,
			created_at timestamptz NOT NULL DEFAULT now()
		);`); err != nil {
		t.Fatalf("ensure findu tables: %v", err)
	}
	ca := newAssemblyAgent(t)
	s := &Server{m: &Manager{pg: pg, dir: t.TempDir()}, chatAgent: ca, chatBusy: map[string]bool{}}
	conv, err := pg.CreateConversation("findu_default", "get test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.DeleteConversation(conv.ID)
	mid := "msg-" + conv.ID
	if _, err := pg.Exec(`INSERT INTO messages (id,conversation_id,role,content,created_at,updated_at) VALUES ($1,$2,'assistant','hello',now(),now())`, mid, conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(`INSERT INTO process_details (id,message_id,conversation_id,event_type,message,data,created_at) VALUES ($1,$2,$3,'response','hello','{}',now())`, "pd-"+conv.ID, mid, conv.ID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+conv.ID+"?include_process_details=1", nil)
	req.SetPathValue("id", conv.ID)
	rec := httptest.NewRecorder()
	s.FinduGetConversation(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"title":"get test"`) || !strings.Contains(rec.Body.String(), `"content":"hello"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestFinduConversationWriteHandlers(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	conv, err := pg.CreateConversation("findu_default", "old", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.DeleteConversation(conv.ID)
	s := &Server{m: &Manager{pg: pg, dir: t.TempDir()}, chatBusy: map[string]bool{}}

	req := httptest.NewRequest(http.MethodPut, "/api/conversations/"+conv.ID, bytes.NewBufferString(`{"title":"new title"}`))
	req.SetPathValue("id", conv.ID)
	rec := httptest.NewRecorder()
	s.FinduUpdateConversation(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"new title"`) {
		t.Fatalf("title status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/conversations/"+conv.ID+"/pinned", bytes.NewBufferString(`{"pinned":true}`))
	req.SetPathValue("id", conv.ID)
	rec = httptest.NewRecorder()
	s.FinduUpdateConversationPinned(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pinned status=%d body=%s", rec.Code, rec.Body.String())
	}
	var pinned bool
	if err := pg.QueryRow(`SELECT pinned_at IS NOT NULL FROM conversations WHERE id=$1`, conv.ID).Scan(&pinned); err != nil || !pinned {
		t.Fatalf("pinned=%v err=%v", pinned, err)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/conversations/"+conv.ID+"/project", bytes.NewBufferString(`{"projectId":""}`))
	req.SetPathValue("id", conv.ID)
	rec = httptest.NewRecorder()
	s.FinduUpdateConversationProject(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("project status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFinduDeleteConversationTurn(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	if _, err := pg.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id text PRIMARY KEY, conversation_id text NOT NULL, role text NOT NULL,
			content text NOT NULL DEFAULT '', mcp_execution_ids text,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
			reasoning_content text
		);
		CREATE TABLE IF NOT EXISTS process_details (
			id text PRIMARY KEY, message_id text NOT NULL, conversation_id text NOT NULL,
			event_type text NOT NULL, message text NOT NULL DEFAULT '', data text,
			created_at timestamptz NOT NULL DEFAULT now()
		);`); err != nil {
		t.Fatal(err)
	}
	conv, err := pg.CreateConversation("findu_default", "delete turn", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.DeleteConversation(conv.ID)
	ids := []string{"u1-" + conv.ID, "a1-" + conv.ID, "u2-" + conv.ID, "a2-" + conv.ID}
	roles := []string{"user", "assistant", "user", "assistant"}
	for i, id := range ids {
		if _, err := pg.Exec(`INSERT INTO messages (id,conversation_id,role,content,created_at,updated_at) VALUES ($1,$2,$3,$4,now()+make_interval(secs => $5),now())`, id, conv.ID, roles[i], id, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pg.Exec(`INSERT INTO process_details (id,message_id,conversation_id,event_type,message,data,created_at) VALUES ($1,$2,$3,'response','x','{}',now())`, "pd-"+conv.ID, ids[1], conv.ID); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{pg: pg, dir: t.TempDir()}, chatBusy: map[string]bool{}}
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/"+conv.ID+"/delete-turn", strings.NewReader(`{"messageId":"`+ids[0]+`"}`))
	req.SetPathValue("id", conv.ID)
	rec := httptest.NewRecorder()
	s.FinduDeleteConversationTurn(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var remaining int
	if err := pg.QueryRow(`SELECT count(*) FROM messages WHERE conversation_id=$1`, conv.ID).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
	var pd int
	if err := pg.QueryRow(`SELECT count(*) FROM process_details WHERE conversation_id=$1`, conv.ID).Scan(&pd); err != nil || pd != 0 {
		t.Fatalf("process details=%d err=%v", pd, err)
	}
}

func TestFinduConversationListCreateDelete(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	if _, err := pg.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id text PRIMARY KEY, conversation_id text NOT NULL, role text NOT NULL,
			content text NOT NULL DEFAULT '', mcp_execution_ids text,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
			reasoning_content text
		);`); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{pg: pg, dir: t.TempDir()}, chatBusy: map[string]bool{}}
	req := httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(`{"title":"crud test","roleName":"默认"}`))
	rec := httptest.NewRecorder()
	s.FinduCreateConversation(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id: %s", rec.Body.String())
	}
	defer pg.DeleteConversation(id)

	// 模拟会话工作目录（chatUpload scope=session 落盘位置），删除后应被清理。
	workDir := filepath.Join(s.m.dir, "sessions", "conv-"+id)
	if err := os.MkdirAll(filepath.Join(workDir, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/conversations?search=crud&limit=10", nil)
	rec = httptest.NewRecorder()
	s.FinduListConversations(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"crud test"`) {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/conversations/"+id, nil)
	req.SetPathValue("id", id)
	rec = httptest.NewRecorder()
	s.FinduDeleteConversation(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	var remaining int
	if err := pg.QueryRow(`SELECT count(*) FROM conversations WHERE id=$1`, id).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("工作目录未被清理: %s (err=%v)", workDir, err)
	}
}

func TestEinoBridgeConversationBindsProjectAndWebshell(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	if _, err := pg.Exec(`CREATE TABLE IF NOT EXISTS projects (
		id text PRIMARY KEY, name text NOT NULL, description text, scope_json text,
		status text NOT NULL DEFAULT 'active', pinned integer NOT NULL DEFAULT 0,
		created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
		owner_user_id text
	)`); err != nil {
		t.Fatal(err)
	}
	projectID := "bridge-project-" + uuid.NewString()
	if _, err := pg.Exec(`INSERT INTO projects (id,name,created_at,updated_at) VALUES ($1,'bridge test',now(),now())`, projectID); err != nil {
		t.Fatal(err)
	}
	defer pg.Exec(`DELETE FROM projects WHERE id=$1`, projectID)
	s := &Server{m: &Manager{pg: pg, dir: t.TempDir()}}
	c, err := s.einoBridgeConversation(finduEinoRequest{
		Message: "bridge binding test", ProjectID: projectID, Role: "渗透测试", WebshellConnectionID: "ws-conn-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.DeleteConversation(c.ID)
	var gotProject, gotRole, gotMode, gotWS, gotAgent string
	if err := pg.QueryRow(`SELECT COALESCE(project_id,''), COALESCE(role_name,''), COALESCE(agent_mode,''),
		COALESCE(webshell_connection_id,''), COALESCE(agent_key,'') FROM conversations WHERE id=$1`, c.ID).
		Scan(&gotProject, &gotRole, &gotMode, &gotWS, &gotAgent); err != nil {
		t.Fatal(err)
	}
	if gotProject != projectID || gotRole != "渗透测试" || gotMode != "eino_single" || gotWS != "ws-conn-1" || gotAgent != "pentest" {
		t.Fatalf("binding project=%q role=%q mode=%q ws=%q agent=%q", gotProject, gotRole, gotMode, gotWS, gotAgent)
	}
}

func TestFinduBatchDeleteCleansMessages(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	ids := make([]string, 0, 2)
	for _, title := range []string{"batch delete one", "batch delete two"} {
		c, err := pg.CreateConversation("mainagent", title, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
		if _, err := pg.Exec(`INSERT INTO messages (id,conversation_id,role,content) VALUES ($1,$2,'user','bye')`,
			"batch-msg-"+c.ID, c.ID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pg.DeleteConversation(c.ID) })
	}
	s := &Server{m: &Manager{pg: pg, dir: t.TempDir()}, chatBusy: map[string]bool{}}
	// 模拟两个会话的工作目录，batch 删除后应被清理。
	workDirs := make([]string, 0, 2)
	for _, id := range ids {
		wd := filepath.Join(s.m.dir, "sessions", "conv-"+id)
		if err := os.MkdirAll(filepath.Join(wd, "uploads"), 0o755); err != nil {
			t.Fatal(err)
		}
		workDirs = append(workDirs, wd)
	}
	body, _ := json.Marshal(map[string]any{"ids": []string{ids[0], ids[1], "missing-batch-id"}})
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/delete/batch", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.pgDeleteConversationsBatch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 3 || !resp.Items[0].OK || !resp.Items[1].OK || resp.Items[2].OK {
		t.Fatalf("batch items=%+v", resp.Items)
	}
	var remaining int
	if err := pg.QueryRow(`SELECT count(*) FROM messages WHERE conversation_id=ANY($1)`, ids).Scan(&remaining); err != nil {
		// The test DB may not expose array binding on its driver; fall back to
		// per-id counts.
		for _, id := range ids {
			var n int
			if err := pg.QueryRow(`SELECT count(*) FROM messages WHERE conversation_id=$1`, id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			remaining += n
		}
	}
	if remaining != 0 {
		t.Fatalf("messages remaining=%d", remaining)
	}
	for _, wd := range workDirs {
		if _, err := os.Stat(wd); !os.IsNotExist(err) {
			t.Fatalf("批量删除后工作目录未被清理: %s (err=%v)", wd, err)
		}
	}
}

func TestFinduConversationExtras(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS model_token_usage (
			id text PRIMARY KEY, process_detail_id text NOT NULL DEFAULT '', message_id text NOT NULL DEFAULT '',
			conversation_id text NOT NULL, project_id text, source text NOT NULL DEFAULT '',
			orchestration text NOT NULL DEFAULT '', reason text NOT NULL DEFAULT '', model text NOT NULL DEFAULT '',
			model_calls integer NOT NULL DEFAULT 0, prompt_tokens integer NOT NULL DEFAULT 0,
			completion_tokens integer NOT NULL DEFAULT 0, total_tokens integer NOT NULL DEFAULT 0,
			cached_tokens integer NOT NULL DEFAULT 0, reasoning_tokens integer NOT NULL DEFAULT 0,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS vulnerabilities (
			id text PRIMARY KEY, conversation_id text NOT NULL, conversation_tag text, task_tag text,
			title text NOT NULL DEFAULT '', description text, severity text, status text,
			vulnerability_type text, target text, preconditions text, reproduction_steps text,
			evidence text, impact text, recommendation text, retest_notes text,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
			project_id text, owner_user_id text
		)`,
	} {
		if _, err := pg.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	c, err := pg.CreateConversation("mainagent", "extras test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pg.Exec(`DELETE FROM model_token_usage WHERE conversation_id=$1`, c.ID)
		_, _ = pg.Exec(`DELETE FROM vulnerabilities WHERE conversation_id=$1`, c.ID)
		_, _ = pg.Exec(`DELETE FROM messages WHERE conversation_id=$1`, c.ID)
		_ = pg.DeleteConversation(c.ID)
	}()
	if _, err := pg.Exec(`INSERT INTO messages (id,conversation_id,role,content) VALUES ($1,$2,'assistant','hello extras')`,
		"msg-"+c.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(`INSERT INTO model_token_usage
		(id,process_detail_id,message_id,conversation_id,source,orchestration,reason,model,model_calls,prompt_tokens,completion_tokens,total_tokens)
		VALUES ($1,'pd-1',$2,$3,'test','eino_single','test','test-model',1,10,20,30)`,
		"usage-"+c.ID, "msg-"+c.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(`INSERT INTO vulnerabilities (id,conversation_id,title,severity,status) VALUES ($1,$2,'extras vuln','high','open')`,
		"vuln-"+c.ID, c.ID); err != nil {
		t.Fatal(err)
	}

	skillDir := t.TempDir()
	planDir := filepath.Join(skillDir, ".eino", "plantask", c.ID)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "1.json"), []byte(`{"id":"1","subject":"first","status":"completed"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "2.json"), []byte(`{"id":"2","subject":"second","status":"in_progress"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{pg: pg, dir: t.TempDir()}, skillDir: skillDir, chatBusy: map[string]bool{}}
	s.chatBusy[s.convBusyKey(c.ID)] = true

	req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+c.ID+"/plan-tasks", nil)
	req.SetPathValue("id", c.ID)
	rec := httptest.NewRecorder()
	s.FinduConversationPlanTasks(rec, req)
	planBody := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(planBody, `"subject":"first"`) ||
		!strings.Contains(planBody, `"activeStep":2`) || !strings.Contains(planBody, `"completed":1`) {
		t.Fatalf("plan-tasks status=%d body=%s", rec.Code, planBody)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/conversations/"+c.ID+"/token-usage", nil)
	req.SetPathValue("id", c.ID)
	rec = httptest.NewRecorder()
	s.FinduConversationTokenUsage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"totalTokens":30`) {
		t.Fatalf("token-usage status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/conversations/"+c.ID+"/results", nil)
	req.SetPathValue("id", c.ID)
	rec = httptest.NewRecorder()
	s.FinduConversationResults(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "hello extras") || !strings.Contains(body, "extras vuln") {
		t.Fatalf("results status=%d body=%s", rec.Code, body)
	}
}

func TestBridgeStopConversationID(t *testing.T) {
	id, ok := bridgeStopConversationID("/api/conversations/abc-123/stop")
	if !ok || id != "abc-123" {
		t.Fatalf("id=%q ok=%v", id, ok)
	}
	for _, path := range []string{
		"/api/conversations/abc/stop/extra",
		"/api/conversations//stop",
		"/api/conversations/abc",
		"/api/conversations/a/b/stop",
	} {
		if _, ok := bridgeStopConversationID(path); ok {
			t.Fatalf("unexpected match: %s", path)
		}
	}
}

func TestEinoAgentBridgeUsesFinduRuntime(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	if _, err := pg.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id text PRIMARY KEY,
			conversation_id text NOT NULL,
			role text NOT NULL,
			content text NOT NULL DEFAULT '',
			mcp_execution_ids text,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			reasoning_content text
		);
		CREATE TABLE IF NOT EXISTS process_details (
			id text PRIMARY KEY,
			message_id text NOT NULL,
			conversation_id text NOT NULL,
			event_type text NOT NULL,
			message text NOT NULL DEFAULT '',
			data text,
			created_at timestamptz NOT NULL DEFAULT now()
		);`); err != nil {
		t.Fatalf("ensure findu tables: %v", err)
	}

	ca := agent.NewChatAgent(
		&assemblyFakeModel{msg: schema.AssistantMessage("bridge answer", nil)},
		"fake-model",
		t.TempDir(),
		transcript.NewStore(t.TempDir()),
		1000,
	)
	s := &Server{
		m:         &Manager{pg: pg, dir: t.TempDir()},
		chatAgent: ca,
		chatBusy:  map[string]bool{},
	}

	body, _ := json.Marshal(map[string]any{
		"message":   "bridge test",
		"role":      "默认",
		"agentMode": "eino_single",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/eino-agent/stream", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.EinoAgentBridge(true).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bridge answer") {
		t.Fatalf("stream body = %s", rec.Body.String())
	}
	var conversationID string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) != nil {
			continue
		}
		if ev["type"] == "conversation" {
			if data, ok := ev["data"].(map[string]any); ok {
				conversationID, _ = data["conversationId"].(string)
			}
		}
	}
	if conversationID == "" {
		t.Fatal("no conversation id in SSE")
	}
	defer pg.DeleteConversation(conversationID)

	var assistant string
	if err := pg.QueryRow(`SELECT content FROM messages WHERE conversation_id=$1 AND role='assistant' ORDER BY created_at DESC LIMIT 1`, conversationID).Scan(&assistant); err != nil {
		t.Fatalf("assistant message: %v", err)
	}
	if assistant != "bridge answer" {
		t.Fatalf("assistant = %q", assistant)
	}
	var details int
	if err := pg.QueryRow(`SELECT count(*) FROM process_details WHERE conversation_id=$1`, conversationID).Scan(&details); err != nil {
		t.Fatalf("process details: %v", err)
	}
	if details == 0 {
		t.Fatal("no process_details written")
	}
}
