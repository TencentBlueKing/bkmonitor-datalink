// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

const (
	SessionKeyPrefix = "scroll:session:"
	LockKeyPrefix    = "scroll:lock:"
	SliceKeyPrefix   = "scroll:slice:"
)

// ScrollSession 用于记录当前scroll的一些meta信息
// 包含查询时间戳、用户名、状态、创建时间、最后访问时间、滚动超时时间、最大切片数、数据源类型等
// 使用query_ts + username作为唯一标识
// 同时也作为创建新的slice的模板
// 如果针对一个查询需要创建多个切片，则可以通过ScrollSession来管理这些切片
type ScrollSession struct {
	Key           string
	CreateAt      time.Time     `json:"create_at"`
	LastAccessAt  time.Time     `json:"last_access_at"`
	ScrollTimeout time.Duration `json:"scroll_timeout"`
	MaxSlice      int           `json:"max_slice"`
	Limit         int           `json:"limit"`
	Index         int           `json:"index"`
	SliceIds      []string      `json:"slice_ids"`
	Status        string        `json:"status"`
	Type          string        `json:"type"` // 数据源类型 es or doris
}

type SliceState struct {
	SessionKey    string        `json:"session_key"`
	StartOffset   int           `json:"start_offset"`
	EndOffset     int           `json:"end_offset"`
	Size          int           `json:"size"`
	Status        string        `json:"status"`
	ErrorMsg      string        `json:"error_msg"`
	RetryCount    int           `json:"retry_count"`
	MaxRetries    int           `json:"max_retries"`
	ScrollID      string        `json:"scroll_id"`
	ConnectInfo   string        `json:"connect_info"`
	SliceMax      int           `json:"slice_max"`
	SliceID       int           `json:"slice_id"`
	LastAccessAt  time.Time     `json:"last_access_at"`
	ScrollTimeOut time.Duration `json:"scroll_timeout"`
	Type          string        `json:"type"`     // 数据源类型 es or doris
	TableID       string        `json:"table_id"` // Table ID for the slice, used to identify the data source
	Connect       string        `json:"connect"`  // Connect information for the slice, used to identify the data source connection
}

type SlicesState []SliceState

func (s *SlicesState) FilterConnect(connect string) []SliceState {
	ss := *s
	filtered := make([]SliceState, 0, len(ss))
	for _, slice := range ss {
		if slice.ConnectInfo == connect {
			filtered = append(filtered, slice)
		}
	}
	return filtered
}

func (s *SliceState) SliceKey() string {
	return fmt.Sprintf(`%s|%d|%d`, s.ScrollID, s.SliceMax, s.SliceID)
}

const (
	SessionStatusRunning = "RUNNING"
	SessionStatusDone    = "DONE"
	SessionStatusFailed  = "FAILED"
)

const (
	SliceStatusRunning = "running"
	SliceStatusDone    = "done"
	SliceStatusFailed  = "failed"
)

var sliceStateKey = func(suffix string) string {
	return fmt.Sprintf("%s|%s", SliceKeyPrefix, suffix)
}

func (s *ScrollSession) getAllSlices(ctx context.Context) (SlicesState, error) {
	slices := make([]SliceState, 0, len(s.SliceIds))
	for _, id := range s.SliceIds {
		key := sliceStateKey(id)
		data, err := globalInstance.client.Get(ctx, key).Bytes()
		if err != nil {
			return nil, fmt.Errorf("failed to get slice %d: %w", id, err)
		}
		var slice SliceState
		if err := json.Unmarshal(data, &slice); err != nil {
			return nil, fmt.Errorf("failed to unmarshal slice %d: %w", id, err)
		}
		slices = append(slices, slice)
	}
	return slices, nil
}

func (s *SlicesState) fetchLastActiveSlice() (*SliceState, bool) {
	if len(*s) == 0 {
		return nil, false
	}
	for i := len(*s) - 1; i >= 0; i-- {
		cur := (*s)[i]
		if cur.Status == SliceStatusRunning || cur.Status == SliceStatusFailed && cur.RetryCount < cur.MaxRetries {
			return &cur, true
		}
	}
	return nil, false
}

// EnsureSlices 确保所有切片都存在，如果不存在则创建新的切片
func (s *ScrollSession) EnsureSlices(ctx context.Context) (SlicesState, error) {
	slices, err := s.getAllSlices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get slices: %w", err)
	}

	if len(slices) >= s.MaxSlice {
		return nil, fmt.Errorf("already have %d slices, max allowed is %d", len(slices), s.MaxSlice)
	}
	if len(slices) == 0 && s.Type == "es" {
		for i := 0; i < s.MaxSlice; i++ {
			newSlice := SliceState{
				SessionKey:  s.Key,
				StartOffset: i * s.Limit,
				EndOffset:   (i + 1) * s.Limit,
				Status:      SliceStatusRunning,
				SliceID:     i,
				SliceMax:    s.MaxSlice,
				ScrollID:    "",
			}
			slices = append(slices, newSlice)
		}
	} else {
		baseOffset := 0
		lastActive, exist := slices.fetchLastActiveSlice()
		if !exist {
			baseOffset = 0
		} else {
			baseOffset = lastActive.EndOffset
		}

		for i := len(slices); i < s.MaxSlice; i++ {
			newSlice := SliceState{
				SessionKey:  "",
				StartOffset: baseOffset + i*s.Limit,
				EndOffset:   baseOffset + (i+1)*s.Limit,
				Status:      SliceStatusRunning,
				SliceID:     i,
				SliceMax:    s.MaxSlice,
				ScrollID:    "",
			}
			slices = append(slices, newSlice)
		}
	}

	return slices, nil
}
