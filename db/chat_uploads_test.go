package db

import (
	"context"
	"testing"
	"time"
)

// TestChatUploadStoreNative 覆盖 Phase B8 的 chat_upload_artifacts 元数据
// upsert/get/删除/重命名（PG 方言与 findu internal/database/chat_upload.go 对齐）。
func TestChatUploadStoreNative(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	ensureFinduCompatFixture(t, d)
	ctx := context.Background()
	cs := d.ChatUploads()

	// 幂等清理上次失败运行残留（仅测试库；生产路径名为 UUID/日期形态）。
	if _, err := d.Exec(`DELETE FROM chat_upload_artifacts WHERE relative_path LIKE 'canary-cu-%' OR relative_path LIKE 'canary-cu-%/%'`); err != nil {
		t.Fatalf("residue cleanup: %v", err)
	}
	cleanup := func() {
		d.Exec(`DELETE FROM chat_upload_artifacts WHERE relative_path LIKE 'canary-cu-%' OR relative_path LIKE 'canary-cu-%/%'`)
	}
	t.Cleanup(cleanup)

	if err := cs.UpsertChatUploadArtifact(ctx, "canary-cu-2026-10-22/conv-a/report.txt", "conv-a", "FindUAI"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := cs.UpsertChatUploadArtifact(ctx, "canary-cu-2026-10-22/conv-a/nested/a.bin", "conv-a", "FindUAI"); err != nil {
		t.Fatalf("upsert nested: %v", err)
	}
	// upsert 幂等更新
	if err := cs.UpsertChatUploadArtifact(ctx, "canary-cu-2026-10-22/conv-a/report.txt", "conv-b", "FindUAI"); err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	conv, owner, ok := cs.GetChatUploadArtifact(ctx, "canary-cu-2026-10-22/conv-a/report.txt")
	if !ok || conv != "conv-b" || owner != "FindUAI" {
		t.Fatalf("get: conv=%s owner=%s ok=%v", conv, owner, ok)
	}
	if _, _, ok := cs.GetChatUploadArtifact(ctx, "canary-cu-missing"); ok {
		t.Fatalf("missing path should not resolve")
	}

	n, err := cs.RenameChatUploadArtifactPath(ctx, "canary-cu-2026-10-22/conv-a/report.txt", "canary-cu-2026-10-22/conv-a/renamed.txt")
	if err != nil || n != 1 {
		t.Fatalf("rename root: n=%d err=%v", n, err)
	}
	// 子树重命名（LIKE 前缀）：conv-a 下的 renamed.txt 与 nested/a.bin 两条都迁移。
	n, err = cs.RenameChatUploadArtifactPath(ctx, "canary-cu-2026-10-22/conv-a", "canary-cu-2026-10-22/conv-b")
	if err != nil || n != 2 {
		t.Fatalf("rename subtree: n=%d err=%v", n, err)
	}
	if _, _, ok := cs.GetChatUploadArtifact(ctx, "canary-cu-2026-10-22/conv-b/nested/a.bin"); !ok {
		t.Fatalf("renamed subtree path should resolve")
	}
	if _, _, ok := cs.GetChatUploadArtifact(ctx, "canary-cu-2026-10-22/conv-b/renamed.txt"); !ok {
		t.Fatalf("renamed root path should resolve")
	}

	n, err = cs.DeleteChatUploadArtifactPath(ctx, "canary-cu-2026-10-22/conv-b")
	if err != nil || n != 2 {
		t.Fatalf("delete subtree: n=%d err=%v", n, err)
	}
	var cnt int
	if err := d.QueryRow(`SELECT COUNT(*) FROM chat_upload_artifacts WHERE relative_path LIKE 'canary-cu-%'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("rows after delete=%d", cnt)
	}

	// 名称投影 SQL（conversations/projects 表有行时才校验；缺省容错为空）
	_ = cs.ConversationProjectID(ctx, "conv-no-such")
	_ = cs.ConversationTitle(ctx, "conv-no-such")
	_ = cs.ProjectName(ctx, "proj-no-such")
	_ = time.Now()
}
