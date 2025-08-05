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
	"sync"
	"time"

	"github.com/pkg/errors"

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
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusFailed    = "failed"
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
	Mu        sync.RWMutex                `json:"-"`
}

func (s *ScrollSession) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

func (s *ScrollSession) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, s)
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

func (s *ScrollSession) Done() bool {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	for _, val := range s.ScrollIDs {
		if val.Status != StatusCompleted {
			return false
		}
	}
	return true
}

func (s *ScrollSession) UpdateSliceStatus(ctx context.Context, sliceKey string, status string, scrollID string) error {
	s.Mu.Lock()
	sliceValue := s.ScrollIDs[sliceKey]
	sliceValue.Status = status
	sliceValue.ScrollID = scrollID

	if status == StatusFailed {
		sliceValue.FailedNum++
	}

	if sliceValue.Status == StatusFailed {
		if sliceValue.FailedNum < s.SliceMaxFailedNum {
			sliceValue.Status = StatusPending
		} else {
			sliceValue.Status = StatusCompleted
		}
	}

	s.ScrollIDs[sliceKey] = sliceValue
	s.LastAccessAt = time.Now()
	s.Mu.Unlock()
	return Client().Set(ctx, s.SessionKey, s, s.ScrollTimeout).Err()
}

func (s *ScrollSession) UpdateSliceStatusAndOffset(ctx context.Context, sliceKey string, status string, scrollID string, offset int) error {
	s.Mu.Lock()
	sliceValue := s.ScrollIDs[sliceKey]
	sliceValue.Status = status
	sliceValue.ScrollID = scrollID
	sliceValue.Offset = offset

	if status == StatusFailed {
		sliceValue.FailedNum++
	}

	s.ScrollIDs[sliceKey] = sliceValue
	s.LastAccessAt = time.Now()
	s.Mu.Unlock()

	return Client().Set(ctx, s.SessionKey, s, s.ScrollTimeout).Err()
}
