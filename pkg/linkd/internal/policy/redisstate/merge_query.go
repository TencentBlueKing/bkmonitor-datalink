// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"context"
	"slices"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/policy"
)

// MergeWindowPage 是当前 Redis 登记快照，读取不会修复、清理或重新创建窗口。
type MergeWindowPage struct {
	Items []MergeWindow
	Next  string
}

// ListMergeWindows 在租户已有的 4096 个登记上限内按窗口身份分页。
// 登记集合可变化，跨页不是一致性快照；已消失窗口不返回，但游标仍越过它。
// Lua 在返回 ID 前验证类型、数量和身份长度，避免损坏数据突破内存预算。
func (s *Store) ListMergeWindows(ctx context.Context, tenant, after string, limit int) (MergeWindowPage, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || (after != "" && !mergeHash(after)) || limit < 1 || limit > 16 {
		return MergeWindowPage{}, policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ids, err := listMergeScript.Run(ctx, s.client, []string{s.mergeDueKey(tenant)}).StringSlice()
	if err != nil {
		return MergeWindowPage{}, err
	}
	if len(ids) > 4096 {
		return MergeWindowPage{}, ErrBudget
	}
	for _, id := range ids {
		if !mergeHash(id) {
			return MergeWindowPage{}, ErrState
		}
	}
	slices.Sort(ids)
	start, exists := slices.BinarySearch(ids, after)
	if exists {
		start++
	}
	end := min(start+limit, len(ids))
	page := MergeWindowPage{Items: []MergeWindow{}}
	for _, id := range ids[start:end] {
		w, found, err := s.ReadMergeWindow(ctx, tenant, id)
		if err != nil {
			return MergeWindowPage{}, err
		}
		if found {
			page.Items = append(page.Items, w)
		}
	}
	if end < len(ids) {
		page.Next = ids[end-1]
	}
	return page, nil
}

var listMergeScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok
if t=='none' then return {} end
if t~='zset' or redis.call('ZCARD',KEYS[1])>4096 then return {'invalid'} end
local ids=redis.call('ZRANGE',KEYS[1],0,4095)
for _,id in ipairs(ids) do if string.len(id)~=64 then return {'invalid'} end end
return ids
`)
