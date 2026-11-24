// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package store

import (
	"context"

	"linkd/internal/domain"
)

// AlertChangeField 标识已完成输出的持久化意图，不能用于修改业务状态。
type AlertChangeField string

const (
	MergeChangeField  AlertChangeField = "merge_change"
	PolicyChangeField AlertChangeField = "policy_change"
)

// AlertChangeCompletionStore 在 current 的存储版本上局部清除意图，返回新的 token。
// 不递增业务 revision；冲突或响应丢失由原意图恢复路径处理，不能无条件确认新意图。
type AlertChangeCompletionStore interface {
	CompleteAlertChange(context.Context, StoredAlert, AlertChangeField) (StoredAlert, error)
}

// CompletedAlertChange 只复制状态字段；来自仓储的未变载荷仍按不可变快照共享。
func CompletedAlertChange(current StoredAlert, field AlertChangeField) (domain.Alert, error) {
	next := current.Alert
	switch field {
	case MergeChangeField:
		next.MergeChange = nil
	case PolicyChangeField:
		next.PolicyChange = nil
	default:
		return domain.Alert{}, ErrInvalidArgument
	}
	return next, nil
}
