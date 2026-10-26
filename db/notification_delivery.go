package db

// 通知投递的领取与状态流转已迁到 domain/notify（Step 3 批 3 第四刀）。
//
// 这里只留方法转发：租约领取（Claim*）、状态流转（Sent/Reschedule/Defer/Fail）、
// 重发与分页列表全部委托给 domain/notify，签名与原实现一致，调用点零改动。
//
// 原实现 446 行（含 claimQuery / selectForClaim / loadDeliveriesTx / truncate 等私有助手），
// 手工语义见 domain/notify/delivery.go 的文件头。

import (
	"context"
	"time"

	dnotify "finduai/domain/notify"
)

func (d *DB) ClaimRealtimeDeliveries(ctx context.Context, channelID int64, limit int, lease time.Duration) ([]*dnotify.NotificationDelivery, error) {
	return dnotify.ClaimRealtimeDeliveries(ctx, d, channelID, limit, lease)
}

func (d *DB) DigestBatchDue(ctx context.Context, channelID int64, minAge time.Duration) (bool, error) {
	return dnotify.DigestBatchDue(ctx, d, channelID, minAge)
}

func (d *DB) ClaimDigestBatch(ctx context.Context, channelID int64, limit int, lease time.Duration) ([]*dnotify.NotificationDelivery, error) {
	return dnotify.ClaimDigestBatch(ctx, d, channelID, limit, lease)
}

func (d *DB) MarkDeliveriesSent(ctx context.Context, ids []int64) error {
	return dnotify.MarkDeliveriesSent(ctx, d, ids)
}

func (d *DB) RescheduleDeliveries(ctx context.Context, ids []int64, delay time.Duration, errMsg string) error {
	return dnotify.RescheduleDeliveries(ctx, d, ids, delay, errMsg)
}

func (d *DB) DeferDeliveries(ctx context.Context, ids []int64, reason string) error {
	return dnotify.DeferDeliveries(ctx, d, ids, reason)
}

func (d *DB) FailDeliveries(ctx context.Context, ids []int64, errMsg string) error {
	return dnotify.FailDeliveries(ctx, d, ids, errMsg)
}

func (d *DB) RetryNotificationDelivery(ctx context.Context, id int64) error {
	return dnotify.RetryNotificationDelivery(ctx, d, id)
}

func (d *DB) ListNotificationDeliveries(ctx context.Context, f dnotify.NotificationDeliveryFilter, page, pageSize int) ([]*dnotify.NotificationDelivery, int, error) {
	return dnotify.ListNotificationDeliveries(ctx, d, f, page, pageSize)
}
