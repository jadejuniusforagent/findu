package db

// 会话域存储下沉（批 3 第六刀）的转发链验证用例：会话线程 CRUD、话步
// 追加/翻页/详情、删除级联，全部走 db 转发打真实 PG。
// pin 语义与 profile 加锁协议由 conversation_pin_test.go / 既有用例钉住。

import (
	"finduai/domain/conversation"
	"testing"

	"finduai/core/model"
)

func TestConversationRepoPackageBridge(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// 创建（nil profile）→ 列表 → patch pin → touch → 重读。
	c, err := d.CreateConversation("bridge", "repo bridge", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteConversation(c.ID) })
	list, err := d.ListConversations()
	if err != nil || len(list) == 0 {
		t.Fatalf("list: n=%d err=%v", len(list), err)
	}
	updated, err := d.UpdateConversation(c.ID, conversation.ConversationPatch{Pinned: boolPtr(true)})
	if err != nil || !updated.Pinned {
		t.Fatalf("pin: %+v err=%v", updated, err)
	}
	if err = d.TouchConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetConversation(c.ID)
	if err != nil || got == nil || got.ID != c.ID || got.AgentKey != "bridge" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	if err = d.RenameConversation(c.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	if got, _ = d.GetConversation(c.ID); got == nil || got.Title != "renamed" {
		t.Fatal("rename lost")
	}

	// 话步：追加两步（含超长 NUL 文本清洗）→ 列表/游标 → 详情懒加载。
	id1, err := d.AppendConvActivity(c.ID, model.Activity{Kind: "user", Summary: "hello\x00"})
	if err != nil || id1 <= 0 {
		t.Fatalf("append: %d %v", id1, err)
	}
	id2, err := d.AppendConvActivity(c.ID, model.Activity{Kind: "result", Detail: "full-blob"})
	if err != nil {
		t.Fatal(err)
	}
	steps, cursor, err := d.ConvActivityList(c.ID, 0, 10)
	if err != nil || len(steps) != 2 || cursor < id2 || steps[0].Summary != "hello" {
		t.Fatalf("list: n=%d cursor=%d first=%+v err=%v", len(steps), cursor, steps[0], err)
	}
	page, hasMore, err := d.ConvActivityPage(c.ID, 0, 1)
	if err != nil || len(page) != 1 || page[0].ID != id2 || !hasMore {
		// 最新一页只有 id2，且还有更早的 id1 可以上滑加载。
		t.Fatalf("page: %+v hasMore=%v err=%v", page, hasMore, err)
	}
	older, hasMore, err := d.ConvActivityPage(c.ID, page[0].ID, 1)
	if err != nil || len(older) != 1 || older[0].ID != id1 || hasMore {
		t.Fatalf("older page: %+v hasMore=%v err=%v", older, hasMore, err)
	}
	detail, err := d.ConvActivityDetail(c.ID, id2)
	if err != nil || detail != "full-blob" {
		t.Fatalf("detail: %q err=%v", detail, err)
	}
	summaries, err := d.ConversationTokenSummaries()
	if err != nil || len(summaries) == 0 {
		t.Fatalf("token summaries: n=%d err=%v", len(summaries), err)
	}

	// 删除级联：会话删除后话步随 FK 消失，DeleteConversations 只报真实存在的 id。
	if deleted, err := d.DeleteConversations([]string{c.ID, "no-such-conv"}); err != nil || len(deleted) != 1 || deleted[0] != c.ID {
		t.Fatalf("delete: %v err=%v", deleted, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM conversation_activities WHERE conversation_id=$1`, c.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("activities survived cascade: %d %v", count, err)
	}
}

func boolPtr(v bool) *bool { return &v }
