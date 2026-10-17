// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"

	"linkd/internal/eventsource"
)

type controlSourceBatchKey struct{}

type controlSourceResult struct {
	record eventsource.Record
	err    error
}

// 批次内只共享来源编辑记录；下一批重新读取，删除来源仍按成员自己的版本选择 Release。
func readControlSource(ctx context.Context, sources closeSourceReader, id string) (eventsource.Record, error) {
	if records, ok := ctx.Value(controlSourceBatchKey{}).(map[string]*controlSourceResult); ok {
		if item, found := records[id]; found {
			return item.record, item.err
		}
	}
	return sources.Get(ctx, id)
}
