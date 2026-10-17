// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package store

import (
	"context"

	"linkd/internal/domain"
)

// AlertSnapshotUpdate 是显式批次中一条已经在成员租约内准备的变更。
type AlertSnapshotUpdate struct {
	Current     StoredAlert
	Replacement domain.Alert
}

// AlertUpdateResult 按输入顺序返回独立结果；成功不承诺其他成员已完成。
type AlertUpdateResult struct {
	StoredAlert
	Err error
}

// AlertSnapshotBatchStore 由调用方明确提交批次，不等待其他请求或承诺跨文档事务。
type AlertSnapshotBatchStore interface {
	CompareAndSetAlertsFromSnapshots(context.Context, []AlertSnapshotUpdate) ([]AlertUpdateResult, error)
	CompleteAlertChanges(context.Context, []StoredAlert, AlertChangeField) ([]AlertUpdateResult, error)
}
