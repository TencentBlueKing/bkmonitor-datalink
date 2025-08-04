// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func generateScrollID(items ...any) string {
	var strs []string
	for _, item := range items {
		if item != nil {
			strs = append(strs, cast.ToString(item))
		}
	}
	return strings.Join(strs, ":")
}

func (i *Instance) MakeSlices(ctx context.Context, session *redis.ScrollSession, connect, tableID string) ([]*redis.SliceInfo, error) {
	var slices []*redis.SliceInfo

	for idx := 0; idx < session.MaxSlice; idx++ {
		key := generateScrollID(consul.BkSqlStorageType, tableID, i)
		session.Mu.RLock()
		val, exists := session.ScrollIDs[key]
		session.Mu.RUnlock()

		if !exists {
			val = redis.SliceStatusValue{
				Status:    redis.StatusPending,
				FailedNum: 0,
				Offset:    idx * i.sliceLimit,
				Limit:     10,
			}
			session.Mu.Lock()
			session.ScrollIDs[key] = val
			session.Mu.Unlock()
			err := session.AtomicUpdateSliceStatus(ctx, key, redis.StatusPending, "")
			if err != nil {
				return nil, fmt.Errorf("failed to initialize slice status: %w", err)
			}
		} else if val.Status == redis.StatusFailed {
			if val.FailedNum < session.SliceMaxFailedNum {
				err := session.AtomicUpdateSliceStatus(ctx, key, redis.StatusPending, val.ScrollID)
				if err != nil {
					return nil, fmt.Errorf("failed to update slice status: %w", err)
				}
				val.Status = redis.StatusPending
			} else {
				err := session.AtomicUpdateSliceStatus(ctx, key, redis.StatusStop, val.ScrollID)
				if err != nil {
					return nil, err
				}
				continue
			}
		} else if val.Status == redis.StatusRunning {
			newOffset := val.Offset + session.MaxSlice*val.Limit
			val.Offset = newOffset
			err := session.AtomicUpdateSliceStatusAndOffset(ctx, key, redis.StatusRunning, val.ScrollID, newOffset)
			if err != nil {
				return nil, err
			}
		} else if val.Status == redis.StatusStop || val.Status == redis.StatusCompleted {
			continue
		}

		slices = append(slices, &redis.SliceInfo{
			Connect:     "",
			TableId:     tableID,
			StorageType: consul.BkSqlStorageType,
			SliceIdx:    idx,
			SliceMax:    session.MaxSlice,
			Offset:      val.Offset,
		})
	}

	return slices, nil
}

func (i *Instance) UpdateScrollStatus(ctx context.Context, session *redis.ScrollSession, connect, tableID string, resultOption *metadata.ResultTableOption, status string) error {
	key := generateScrollID(consul.BkSqlStorageType, tableID, resultOption.SliceIndex)

	var scrollID string
	if val, exists := session.ScrollIDs[key]; exists {
		scrollID = val.ScrollID
	}

	return session.AtomicUpdateSliceStatus(ctx, key, status, scrollID)
}
