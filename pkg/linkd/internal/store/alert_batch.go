// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package store

import (
	"context"

	"linkd/internal/domain"
)

// MaxCloseBatchSize 是只返回候选 ID 的关闭请求上限；不同于返回完整告警的普通分页。
const MaxCloseBatchSize = 5000

// AlertCloseBatchStore 只条件更新主动关闭字段，不重新发送冻结的正文和丰富数据。
type AlertCloseBatchStore interface {
	CloseAlertsFromSnapshots(context.Context, []AlertSnapshotUpdate) ([]AlertUpdateResult, error)
}

// AlertIdentityBatchStore 只读关闭租约所需的身份；返回的 Alert 不含完整业务快照，不能用于 CAS。
type AlertIdentityBatchStore interface {
	GetAlertIdentities(context.Context, string, []string) (AlertBatch, error)
}

// AlertCloseStateBatchStore 只读取关闭裁决、清理和轻量输出需要的状态，不包含正文与丰富快照。
type AlertCloseStateBatchStore interface {
	GetAlertCloseStates(context.Context, string, []string) (AlertBatch, error)
}

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
