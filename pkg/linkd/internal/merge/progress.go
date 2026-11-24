// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import "context"

// progressDocuments 复用既有文档 CAS，只传本次变化的进度和成员状态。
type progressDocuments interface {
	CompareAndSetMergeProgress(context.Context, string, string, string, map[string]any, map[int]string, bool) (string, error)
}
