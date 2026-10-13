// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"context"
	"errors"
)

// ReadWorkPage 在明确的载荷超限时缩小单页；游标由调用方固定，不能跳过大记录。
// 页大小降到一条后仍失败则返回原错误，不隐藏损坏数据或用忙循环重试。
func ReadWorkPage[T any](ctx context.Context, limit int, read func(context.Context, int) (T, error)) (T, int, error) {
	for {
		page, err := read(ctx, limit)
		if !errors.Is(err, ErrPageTooLarge) || limit <= 1 || ctx.Err() != nil {
			return page, limit, err
		}
		limit = max(1, limit/2)
	}
}
