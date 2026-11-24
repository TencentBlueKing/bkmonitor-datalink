// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"

	"linkd/internal/store"
)

// snapshotCacheWriter 仅接收 Repository 已校验的读取或成功写入快照。
type snapshotCacheWriter interface {
	PutCurrentFromSnapshot(context.Context, store.StoredAlert) error
	PutTerminalFromSnapshot(context.Context, store.StoredAlert) error
}

type snapshotCache struct {
	RecentAlertCache
	writer snapshotCacheWriter
}

// snapshotWrites 只替换可信写入；读取、修复和普通后端沿用原契约。
func snapshotWrites(cache RecentAlertCache) RecentAlertCache {
	if writer, ok := cache.(snapshotCacheWriter); ok {
		return snapshotCache{RecentAlertCache: cache, writer: writer}
	}
	return cache
}

func (c snapshotCache) PutCurrent(ctx context.Context, stored store.StoredAlert) error {
	return c.writer.PutCurrentFromSnapshot(ctx, stored)
}

func (c snapshotCache) PutTerminal(ctx context.Context, stored store.StoredAlert) error {
	return c.writer.PutTerminalFromSnapshot(ctx, stored)
}

// PutCurrentSnapshots 只用于同一来源的显式缓存批；其他缓存实现保留单项能力。
func (c snapshotCache) PutCurrentSnapshots(ctx context.Context, current []store.StoredAlert) []error {
	if writer, ok := c.writer.(interface {
		PutCurrentSnapshots(context.Context, []store.StoredAlert) []error
	}); ok {
		return writer.PutCurrentSnapshots(ctx, current)
	}
	results := make([]error, len(current))
	for i, item := range current {
		results[i] = c.writer.PutCurrentFromSnapshot(ctx, item)
	}
	return results
}
