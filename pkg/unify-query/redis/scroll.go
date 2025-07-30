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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

var (
	ErrorOfUnSupportScrollStorageType = errors.New("UnSupportScrollStorageType")
	ErrorOfScrollSliceStatusNotFound  = errors.New("ScrollSliceStatusNotFound")
)

const (
	DefaultSliceMaxFailedNum = 3
)

const (
	SessionKeyPrefix = "scroll:session:"
	LockKeyPrefix    = "scroll:lock:"
)

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusFailed    = "failed"
	StatusStop      = "stop" // 超过失败次数限制
	StatusCompleted = "completed"
)

type ScrollSession struct {
	SessionKey        string        `json:"session_key"`
	LastAccessAt      time.Time     `json:"last_access_at"`
	ScrollTimeout     time.Duration `json:"scroll_timeout"`
	MaxSlice          int           `json:"max_slice"`
	SliceMaxFailedNum int           `json:"slice_max_failed_num"`
	Limit             int           `json:"limit"`
	Index             int           `json:"index"`
	// map key is SliceStatusKey
	ScrollIDs map[string]SliceStatusValue `json:"scroll_ids"`
	Status    string                      `json:"status"`
}

func (s *ScrollSession) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

func (s *ScrollSession) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, s)
}

func generateScrollSliceStatusKey(storageType, connect, tableID string, sliceIdx int) string {
	return fmt.Sprintf("%s:%s:%s:%d", storageType, connect, tableID, sliceIdx)
}

func NewScrollSession(sessionKeySuffix string, maxSlice int, scrollTimeout time.Duration, limit int) *ScrollSession {
	sessionKey := SessionKeyPrefix + sessionKeySuffix
	return &ScrollSession{
		SessionKey:        sessionKey,
		LastAccessAt:      time.Now(),
		ScrollTimeout:     scrollTimeout,
		MaxSlice:          maxSlice,
		SliceMaxFailedNum: DefaultSliceMaxFailedNum,
		Limit:             limit,
		Index:             0,
		ScrollIDs:         make(map[string]SliceStatusValue),
		Status:            StatusRunning,
	}
}

type SliceStatusKey struct {
	StorageType string `json:"storagetype"`
	Connect     string `json:"connect"`
	TableID     string `json:"table_id"`
	SliceIdx    int    `json:"slice_idx"`
}

type SliceStatusValue struct {
	ScrollID  string `json:"scroll_id"`
	Offset    int    `json:"offset"`
	Status    string `json:"status"`
	FailedNum int    `json:"failed_num"`
	Limit     int    `json:"limit"`
}

type SliceInfo struct {
	Connect     string
	TableId     string
	StorageType string
	SliceIdx    int
	SliceMax    int
	ScrollID    string
	Offset      int
}

func (s *ScrollSession) MakeSlices(storageType, connect, tableID string) (slices []*SliceInfo, err error) {
	switch storageType {
	case consul.ElasticsearchStorageType:
		return s.makeESSlices(connect, tableID)
	case consul.BkSqlStorageType:
		return s.makeDorisSlices(connect, tableID)
	default:
		return nil, ErrorOfUnSupportScrollStorageType
	}
}

func (s *ScrollSession) makeESSlices(connect, tableID string) (slices []*SliceInfo, err error) {
	needUpdate := false
	for i := 0; i < s.MaxSlice; i++ {
		key := generateScrollSliceStatusKey(consul.ElasticsearchStorageType, connect, tableID, i)
		val, exists := s.ScrollIDs[key]

		if !exists {
			val = SliceStatusValue{
				Status:    StatusPending,
				FailedNum: 0,
			}
			s.ScrollIDs[key] = val
			needUpdate = true
		}

		if val.Status == StatusFailed {
			if val.FailedNum < s.SliceMaxFailedNum {
				val.Status = StatusPending
				s.ScrollIDs[key] = val
				needUpdate = true
			} else {
				val.Status = StatusStop
				s.ScrollIDs[key] = val
				needUpdate = true
				continue
			}
		}

		if val.Status == StatusStop || val.Status == StatusCompleted {
			continue
		}

		slices = append(slices, &SliceInfo{
			Connect:     connect,
			TableId:     tableID,
			StorageType: consul.ElasticsearchStorageType,
			SliceIdx:    i,
			SliceMax:    s.MaxSlice,
			ScrollID:    val.ScrollID,
		})
	}

	if needUpdate {
		err = Client().Set(context.Background(), s.SessionKey, s, s.ScrollTimeout).Err()
	}

	return
}

func (s *ScrollSession) makeDorisSlices(connect, tableID string) (slices []*SliceInfo, err error) {
	needUpdate := false
	for i := 0; i < s.MaxSlice; i++ {
		key := generateScrollSliceStatusKey(consul.BkSqlStorageType, "", tableID, i)
		val, exists := s.ScrollIDs[key]

		if !exists {
			val = SliceStatusValue{
				Status:    StatusPending,
				FailedNum: 0,
				Offset:    i * 10,
				Limit:     10,
			}
			s.ScrollIDs[key] = val
			needUpdate = true
		}

		if val.Status == StatusFailed {
			if val.FailedNum < s.SliceMaxFailedNum {
				val.Status = StatusPending
				s.ScrollIDs[key] = val
				needUpdate = true
			} else {
				val.Status = StatusStop
				s.ScrollIDs[key] = val
				needUpdate = true
				continue
			}
		}

		if val.Status == StatusStop || val.Status == StatusCompleted {
			continue
		}

		slices = append(slices, &SliceInfo{
			Connect:     "",
			TableId:     tableID,
			StorageType: consul.BkSqlStorageType,
			SliceIdx:    i,
			SliceMax:    s.MaxSlice,
			Offset:      val.Offset,
		})
	}

	if needUpdate {
		err = Client().Set(context.Background(), s.SessionKey, s, s.ScrollTimeout).Err()
	}

	return
}

func (s *ScrollSession) RollDoris(ctx context.Context, tableID string, scrollIndex *int) error {
	sliceKey := generateScrollSliceStatusKey(consul.BkSqlStorageType, "", tableID, *scrollIndex)
	sliceStatusValue, ok := s.ScrollIDs[sliceKey]
	if !ok {
		return ErrorOfScrollSliceStatusNotFound
	}

	sliceStatusValue.Status = StatusRunning
	sliceStatusValue.Offset += s.MaxSlice * sliceStatusValue.Limit
	return s.updateScrollSliceStatusValue(ctx, sliceKey, sliceStatusValue)
}

func (s *ScrollSession) UpdateScrollID(ctx context.Context, connect, tableID, scrollID string, scrollIndex *int, status string) error {
	key := generateScrollSliceStatusKey(consul.ElasticsearchStorageType, connect, tableID, *scrollIndex)
	sliceStatusValue, ok := s.ScrollIDs[key]
	if !ok {
		return ErrorOfScrollSliceStatusNotFound
	}

	sliceStatusValue.Status = status
	if status == StatusFailed {
		sliceStatusValue.FailedNum++
	}

	sliceStatusValue.ScrollID = scrollID
	return s.updateScrollSliceStatusValue(ctx, key, sliceStatusValue)
}

func (s *ScrollSession) CouldDone() bool {
	for _, val := range s.ScrollIDs {
		if val.Status != StatusCompleted && val.Status != StatusStop {
			return false
		}
	}
	return true
}

func (s *ScrollSession) updateScrollSliceStatusValue(ctx context.Context, sliceKey string, value SliceStatusValue) error {
	s.ScrollIDs[sliceKey] = value
	s.LastAccessAt = time.Now()
	return Client().Set(ctx, s.SessionKey, s, s.ScrollTimeout).Err()
}

func (s *ScrollSession) UpdateDoris(ctx context.Context, tableID string, sliceIndex *int, status string) error {
	key := generateScrollSliceStatusKey(consul.BkSqlStorageType, "", tableID, *sliceIndex)
	sliceStatusValue, ok := s.ScrollIDs[key]
	if !ok {
		return ErrorOfScrollSliceStatusNotFound
	}

	sliceStatusValue.Status = status
	if status == StatusFailed {
		sliceStatusValue.FailedNum++
	}

	return s.updateScrollSliceStatusValue(ctx, key, sliceStatusValue)
}
