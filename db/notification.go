package db

// 通知（IM 推送）的存储层已迁到 domain/notify（Step 3 批 3 第四刀）。
//
// 本文件保留三部分：
//  1. 类型/常量别名 —— 调用点（server/*、agent/*、dispatcher）零改动；
//  2. 方法转发 —— 渠道 CRUD、事件、分派、投递状态机全部委托给 domain/notify；
//  3. 两个跨域函数 —— 它们不属于通知域，留在装配层：
//     NotificationAssetNames 读 assets（资产域），
//     SetFindingStatusWithNotify / SetFindingStatusTx 写 findings（漏洞域）。
//     让通知域反向认识别的域会把边界搞乱，所以这两个不搬。
//
// 原实现：db/notification.go 557 行 + db/notification_delivery.go 446 行。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	dnotify "finduai/domain/notify"
	"finduai/notify"
)

// ---- 渠道（表 notification_channels）----

func (d *DB) ListNotificationChannels(ctx context.Context) ([]*dnotify.NotificationChannel, error) {
	return dnotify.ListNotificationChannels(ctx, d)
}

func (d *DB) NotificationChannelByID(ctx context.Context, id int64) (*dnotify.NotificationChannel, error) {
	return dnotify.NotificationChannelByID(ctx, d, id)
}

func (d *DB) SaveNotificationChannel(ctx context.Context, c *dnotify.NotificationChannel) (int64, error) {
	return dnotify.SaveNotificationChannel(ctx, d, c)
}

// SetNotificationChannelEnabled 切换启停。事务由装配层开（WithEvidenceTx），
// 业务语义（停用时把未发投递置 skipped）在 domain/notify。
func (d *DB) SetNotificationChannelEnabled(ctx context.Context, id int64, enabled bool) error {
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		return dnotify.SetChannelEnabledTx(ctx, tx, id, enabled)
	})
}

func (d *DB) DeleteNotificationChannel(ctx context.Context, id int64) error {
	return dnotify.DeleteNotificationChannel(ctx, d, id)
}

// ---- 事件（表 notification_events）----

func (d *DB) AddNotificationEvent(ctx context.Context, kind string, findingID int64, snap notify.Snapshot) (int64, error) {
	return dnotify.AddNotificationEvent(ctx, d, kind, findingID, snap)
}

// FanOutPendingEvents 把未分派事件按启用渠道展开成投递任务。
func (d *DB) FanOutPendingEvents(ctx context.Context, limit int) (eventCount, deliveryCount int, err error) {
	return dnotify.FanOutPendingEvents(ctx, d, limit)
}

func (d *DB) NotificationStatsSnapshot(ctx context.Context) (*dnotify.NotificationStats, error) {
	return dnotify.NotificationStatsSnapshot(ctx, d)
}

// ---- 跨域函数（刻意留在装配层）----

func (d *DB) NotificationAssetNames(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	q := `SELECT id, type, domain, ip, url, app_name, bundle_id FROM assets WHERE id IN (` + strings.Join(ph, ",") + `)`
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := map[string]string{}
	for rows.Next() {
		var (
			id                string
			typ               string
			domain, ip, url   sql.NullString
			appName, bundleID sql.NullString
		)
		if err := rows.Scan(&id, &typ, &domain, &ip, &url, &appName, &bundleID); err != nil {
			return nil, err
		}
		labels[id] = assetDisplayName(typ, domain.String, ip.String, url.String, appName.String, bundleID.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if label, ok := labels[id]; ok && label != "" {
			out = append(out, label)
		}
	}
	return out, nil
}

func assetDisplayName(typ, domain, ip, url, appName, bundleID string) string {
	pick := func(vals ...string) string {
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	switch typ {
	case "root_domain", "subdomain":
		return domain
	case "ip":
		return ip
	case "app":
		return pick(appName, bundleID)
	case "service", "endpoint":
		return pick(url, domain, ip)
	default:
		return pick(domain, ip, url, appName)
	}
}

func (d *DB) SetFindingStatusWithNotify(ctx context.Context, id int64, status string) (from string, found bool, notified bool, err error) {
	err = d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var txErr error
		from, found, _, notified, txErr = SetFindingStatusTx(ctx, tx, id, status)
		return txErr
	})
	return from, found, notified, err
}

func SetFindingStatusTx(ctx context.Context, tx *sql.Tx, id int64, status string) (from string, found bool, changed bool, notified bool, err error) {
	var (
		vulnclass, name, severity, summary string
		taskID                             sql.NullInt64
		assetIDs                           []byte
	)
	scanErr := tx.QueryRowContext(ctx, `SELECT vulnclass, name, severity, summary, task_id, asset_ids, status
FROM findings WHERE id=$1 FOR UPDATE`, id).
		Scan(&vulnclass, &name, &severity, &summary, &taskID, &assetIDs, &from)
	if scanErr == sql.ErrNoRows {
		return "", false, false, false, nil
	}
	if scanErr != nil {
		return "", false, false, false, scanErr
	}
	found = true
	if from == status {
		// 状态没有真的变化就不登记事件：重复提交同一个值、幂等重放都不该
		// 产生推送噪音。
		return from, true, false, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET status=$2 WHERE id=$1`, id, status); err != nil {
		return from, true, false, false, err
	}
	var assets []string
	_ = json.Unmarshal(assetIDs, &assets)
	notified = dnotify.RecordNotificationEventTx(ctx, tx, notify.EventFindingStatusChanged, id, notify.Snapshot{
		Kind:       notify.EventFindingStatusChanged,
		FindingID:  id,
		TaskID:     taskID.Int64,
		VulnClass:  vulnclass,
		Name:       name,
		Severity:   severity,
		Summary:    summary,
		AssetIDs:   assets,
		FromStatus: from,
		ToStatus:   status,
	})
	return from, true, true, notified, nil
}
