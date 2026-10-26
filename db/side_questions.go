package db

// 旁路问答的存储层已迁到 domain/conversation（Step 3 批 3 第五刀）。
// 本文件保留类型别名与方法转发；父会话 FOR SHARE 锁、**ErrSideParentGone**
// 删除语义、generation 版本控制都在 domain/conversation/sidequestions.go。

import (
	"context"

	dconv "finduai/domain/conversation"
	"finduai/sidequestion"
)

func (d *DB) SaveSideSnapshot(ctx context.Context, s sidequestion.Snapshot) error {
	return dconv.SaveSideSnapshot(ctx, d, s)
}

func (d *DB) SideSnapshot(ctx context.Context, key string) (*sidequestion.Snapshot, error) {
	return dconv.SideSnapshot(ctx, d, key)
}

func (d *DB) ExistingSideRequest(ctx context.Context, key, client string) (*sidequestion.Exchange, error) {
	return dconv.ExistingSideRequest(ctx, d, key, client)
}

func (d *DB) SideRequest(ctx context.Context, id string) (*sidequestion.Exchange, error) {
	return dconv.SideRequest(ctx, d, id)
}

func (d *DB) CurrentSideRequest(ctx context.Context, key string) (*sidequestion.Exchange, error) {
	return dconv.CurrentSideRequest(ctx, d, key)
}

func (d *DB) SideHistory(ctx context.Context, key string, before int64, limit int) ([]sidequestion.Exchange, error) {
	return dconv.SideHistory(ctx, d, key, before, limit)
}

func (d *DB) SideReplay(ctx context.Context, key string) ([]sidequestion.Exchange, error) {
	return dconv.SideReplay(ctx, d, key)
}

func (d *DB) StartSideRequest(ctx context.Context, s sidequestion.Snapshot, clientID, question string) (*sidequestion.Exchange, bool, error) {
	return dconv.StartSideRequest(ctx, d, s, clientID, question)
}

func (d *DB) UpdateSideRequest(ctx context.Context, e sidequestion.Exchange) (bool, error) {
	return dconv.UpdateSideRequest(ctx, d, e)
}

func (d *DB) ClearSideHistory(ctx context.Context, key string) error {
	return dconv.ClearSideHistory(ctx, d, key)
}

func (d *DB) SideMemory(ctx context.Context, e sidequestion.Exchange) (sidequestion.Memory, error) {
	return dconv.SideMemory(ctx, d, e)
}

func (d *DB) SideReplayPage(ctx context.Context, e sidequestion.Exchange, after int64) ([]sidequestion.Exchange, error) {
	return dconv.SideReplayPage(ctx, d, e, after)
}

func (d *DB) SaveSideMemory(ctx context.Context, e sidequestion.Exchange, memory sidequestion.Memory) error {
	return dconv.SaveSideMemory(ctx, d, e, memory)
}

func (d *DB) InterruptSideRequests(ctx context.Context) error {
	return dconv.InterruptSideRequests(ctx, d)
}
