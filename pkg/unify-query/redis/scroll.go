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
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

const (
	SessionKeyPrefix = "scroll:session:"
	LockKeyPrefix    = "scroll:lock:"
)

type ScrollSession struct {
	Key           string
	CreateAt      time.Time     `json:"create_at"`
	LastAccessAt  time.Time     `json:"last_access_at"`
	ScrollTimeout time.Duration `json:"scroll_timeout"`
	MaxSlice      int           `json:"max_slice"`
	Limit         int           `json:"limit"`
	Index         int           `json:"index"`
	// key: connect|tableID|sliceIdx, value: 当前有效的 scrollID
	ScrollIDs map[string]string `json:"scroll_ids"`
	// key: connect|tableID|sliceIdx, value: 是否已完成
	SliceStatus map[string]bool `json:"slice_status"`
	Status      string          `json:"status"`
}

const (
	SessionStatusRunning = "RUNNING"
	SessionStatusDone    = "DONE"
	SessionStatusFailed  = "FAILED"
)

func (s *ScrollSession) CurrentScrollID(ctx context.Context, storageType, connect, tableID string, sliceIdx int) (string, int, error) {
	switch storageType {
	case consul.ElasticsearchStorageType:
		return s.getNextElasticsearchScrollID(ctx, connect, tableID, sliceIdx)
	case consul.BkSqlStorageType:
		return s.getNextDorisIndex(ctx)
	default:
		return "", 0, fmt.Errorf("unsupported storage type for scroll: %s", storageType)
	}
}

func (s *ScrollSession) getNextDorisIndex(ctx context.Context) (string, int, error) {
	currentIndex := s.Index
	s.Index++
	return "", currentIndex, nil
}

func (s *ScrollSession) getNextElasticsearchScrollID(ctx context.Context, connect, tableID string, sliceIdx int) (string, int, error) {
	mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)

	scrollID, exist := s.ScrollIDs[mapKey]
	if !exist {
		return "", 0, nil
	}

	return scrollID, 0, nil
}

func (s *ScrollSession) SetScrollID(connect, tableID, scrollID string, sliceIdx int) {
	if s.ScrollIDs == nil {
		s.ScrollIDs = make(map[string]string)
	}

	mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
	s.ScrollIDs[mapKey] = scrollID
}

func (s *ScrollSession) MarkSliceDone(connect, tableID string, sliceIdx int) {
	if s.SliceStatus == nil {
		s.SliceStatus = make(map[string]bool)
	}

	sliceKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
	s.SliceStatus[sliceKey] = true
}

func (s *ScrollSession) RemoveScrollID(connect, tableID string, sliceIdx int) {
	mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
	delete(s.ScrollIDs, mapKey)
}

func (s *ScrollSession) HasMoreData(tsDbType string) bool {
	switch tsDbType {
	case consul.ElasticsearchStorageType:
		if s.SliceStatus == nil {
			s.SliceStatus = make(map[string]bool)
		}

		if len(s.ScrollIDs) > 0 {
			return true
		}

		connectTablePairs := make(map[string]bool)
		for sliceKey := range s.SliceStatus {
			parts := strings.Split(sliceKey, "|")
			if len(parts) >= 3 {
				connectTable := fmt.Sprintf("%s|%s", parts[0], parts[1])
				connectTablePairs[connectTable] = true
			}
		}

		for connectTable := range connectTablePairs {
			completedSlices := 0
			for sliceIdx := 0; sliceIdx < s.MaxSlice; sliceIdx++ {
				sliceKey := fmt.Sprintf("%s|%d", connectTable, sliceIdx)
				if s.SliceStatus[sliceKey] {
					completedSlices++
				}
			}

			if completedSlices < s.MaxSlice {
				return true
			}
		}

		return false
	case consul.BkSqlStorageType:
		return s.Status != SessionStatusDone
	default:
		return false
	}
}
