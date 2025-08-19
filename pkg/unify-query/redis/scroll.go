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

const (
	SessionKeyPrefix    = "scroll:session:"
	ScrollLockKeyPrefix = "scroll:lock:"
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
	SessionKey        string             `json:"session_key"`
	LockKey           string             `json:"lock_key"`
	LastAccessAt      time.Time          `json:"last_access_at"`
	ScrollTimeout     time.Duration      `json:"scroll_timeout"`
	MaxSlice          int                `json:"max_slice"`
	SliceMaxFailedNum int                `json:"slice_max_failed_num"`
	Limit             int                `json:"limit"`
	ScrollIDs         []SliceStatusValue `json:"scroll_ids"`

	Mu sync.RWMutex `json:"-"`
}

func (s *ScrollSession) UpdateSliceStatus(idx int, value SliceStatusValue) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.ScrollIDs[idx] = value
	s.LastAccessAt = time.Now()
}

func newScrollSession(queryTsStr string, scrollTimeout time.Duration, maxSlice, sliceMaxFailedNum, Limit int) *ScrollSession {
	session := &ScrollSession{
		SessionKey:        SessionKeyPrefix + queryTsStr,
		LockKey:           ScrollLockKeyPrefix + queryTsStr,
		LastAccessAt:      time.Now(),
		ScrollTimeout:     scrollTimeout,
		MaxSlice:          maxSlice,
		SliceMaxFailedNum: sliceMaxFailedNum,
		Limit:             Limit,
		ScrollIDs:         []SliceStatusValue{},
		Mu:                sync.RWMutex{},
	}
	session.makeSlices()
	return session
}

func GetOrCreateScrollSession(ctx context.Context, queryTsStr string, scrollTimeout string, maxSlice, sliceMaxFailedNum, Limit int) (*ScrollSession, error) {
	session, exist := checkScrollSession(ctx, queryTsStr)
	if exist {
		return session, nil
	}
	scrollTimeoutDuration, err := time.ParseDuration(scrollTimeout)
	if err != nil {
		return nil, err
	}
	return newScrollSession(queryTsStr, scrollTimeoutDuration, maxSlice, sliceMaxFailedNum, Limit), nil
}

func checkScrollSession(ctx context.Context, queryTsStr string) (*ScrollSession, bool) {
	session := &ScrollSession{}
	err := Client().Get(ctx, SessionKeyPrefix+queryTsStr).Scan(session)
	if err != nil {
		return nil, false
	}

	return session, true
}

func (s *ScrollSession) AcquireLock(ctx context.Context) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	err := Client().SetNX(ctx, s.LockKey, "locked", s.ScrollTimeout).Err()
	if err != nil {
		return errors.Wrap(err, "failed to acquire lock")
	}

	s.LastAccessAt = time.Now()
	return Client().Set(ctx, s.SessionKey, s, s.ScrollTimeout).Err()
}

func (s *ScrollSession) ReleaseLock(ctx context.Context) (err error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	err = Client().Del(ctx, s.LockKey).Err()
	if err != nil {
		return
	}
	s.LastAccessAt = time.Now()
	err = Client().Set(ctx, s.SessionKey, s, s.ScrollTimeout).Err()
	if err != nil {
		return
	}
	return
}

func (s *ScrollSession) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

func (s *ScrollSession) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, s)
}

type SliceStatusKey struct {
	StorageType string `json:"storagetype"`
	Connect     string `json:"connect"`
	TableID     string `json:"table_id"`
	SliceIdx    int    `json:"slice_idx"`
}

type SliceStatusValue struct {
	SliceIdx int `json:"slice_idx"`
	SliceMax int `json:"slice_max"`

	ScrollID  string `json:"scroll_id"`
	Offset    int    `json:"offset"`
	Status    string `json:"status"`
	FailedNum int    `json:"failed_num"`
	Limit     int    `json:"limit"`
}

func (s *SliceStatusValue) Done() bool {
	return s.Status == StatusCompleted || s.FailedNum >= DefaultSliceMaxFailedNum
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

func (s *ScrollSession) makeSlices() []SliceStatusValue {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	for idx := 0; idx < s.MaxSlice; idx++ {
		sliceValue := SliceStatusValue{
			SliceIdx:  idx,
			SliceMax:  s.MaxSlice,
			ScrollID:  "",
			Offset:    idx * s.Limit,
			Status:    StatusPending,
			FailedNum: 0,
			Limit:     s.Limit,
		}
		s.ScrollIDs = append(s.ScrollIDs, sliceValue)
	}

	return s.ScrollIDs
}

func (s *ScrollSession) Done() bool {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	for _, sliceValue := range s.ScrollIDs {
		if sliceValue.Status != StatusCompleted && sliceValue.FailedNum < s.SliceMaxFailedNum {
			return false
		}
	}
	return true
}
