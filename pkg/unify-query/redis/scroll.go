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
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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
	Ctx               context.Context `json:"-"`
	SessionKey        string          `json:"session_key"`
	LockKey           string          `json:"lock_key"`
	LastAccessAt      time.Time       `json:"last_access_at"`
	ScrollTimeout     time.Duration   `json:"scroll_timeout"`
	MaxSlice          int             `json:"max_slice"`
	SliceMaxFailedNum int             `json:"slice_max_failed_num"`
	Limit             int             `json:"limit"`
	// map key is SliceStatusKey
	ScrollIDs map[string]SliceStatusValue `json:"scroll_ids"`
	Mu        sync.RWMutex                `json:"-"`
}

func (s *ScrollSession) UpdateSliceStatus(key string, value SliceStatusValue) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.ScrollIDs[key] = value
	s.LastAccessAt = time.Now()
}

func newScrollSession(ctx context.Context, queryTsStr string, scrollTimeout time.Duration, maxSlice, sliceMaxFailedNum, Limit int) *ScrollSession {
	session := &ScrollSession{
		Ctx:               ctx,
		SessionKey:        SessionKeyPrefix + queryTsStr,
		LockKey:           ScrollLockKeyPrefix + queryTsStr,
		LastAccessAt:      time.Now(),
		ScrollTimeout:     scrollTimeout,
		MaxSlice:          maxSlice,
		SliceMaxFailedNum: sliceMaxFailedNum,
		Limit:             Limit,
		ScrollIDs:         make(map[string]SliceStatusValue),
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
	return newScrollSession(ctx, queryTsStr, scrollTimeoutDuration, maxSlice, sliceMaxFailedNum, Limit), nil
}

func checkScrollSession(ctx context.Context, queryTsStr string) (*ScrollSession, bool) {
	var session ScrollSession
	err := Client().Get(ctx, SessionKeyPrefix+queryTsStr).Scan(&session)
	if err != nil {
		return nil, false
	} else {
		session.Ctx = ctx
		return &session, true
	}
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

func (s *ScrollSession) ReleaseLock() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	Client().Del(s.Ctx, s.LockKey).Err()
	s.LastAccessAt = time.Now()
	Client().Set(s.Ctx, s.SessionKey, s, s.ScrollTimeout).Err()
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

type SliceInfo struct {
	Connect     string
	TableId     string
	StorageType string
	SliceIdx    int
	SliceMax    int
	ScrollID    string
	Offset      int
}

func (s *ScrollSession) makeSlices() []*SliceInfo {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	var slices []*SliceInfo

	if len(s.ScrollIDs) > 0 {
		for _, sliceValue := range s.ScrollIDs {
			slice := &SliceInfo{
				SliceIdx:    sliceValue.SliceIdx,
				SliceMax:    sliceValue.SliceMax,
				Offset:      sliceValue.Offset,
				Connect:     "",
				TableId:     "",
				StorageType: "",
				ScrollID:    sliceValue.ScrollID,
			}
			slices = append(slices, slice)
		}
		return slices
	}

	for idx := 0; idx < s.MaxSlice; idx++ {
		sliceKey := fmt.Sprintf("slice_%d", idx)
		sliceValue := SliceStatusValue{
			SliceIdx:  idx,
			SliceMax:  s.MaxSlice,
			ScrollID:  "",
			Offset:    0,
			Status:    StatusPending,
			FailedNum: 0,
			Limit:     s.Limit,
		}
		s.ScrollIDs[sliceKey] = sliceValue

		slice := &SliceInfo{
			SliceIdx: idx,
			SliceMax: s.MaxSlice,
			Offset:   0,
		}
		slices = append(slices, slice)
	}

	return slices
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

func (s *SliceInfo) Update(opt *metadata.ResultTableOption) error {
	s.ScrollID = opt.ScrollID
	if opt.SliceIndex != nil {
		s.SliceIdx = *opt.SliceIndex
	}
	if opt.SliceMax != nil {
		s.SliceMax = *opt.SliceMax
	}
	if opt.From != nil {
		s.Offset = *opt.From
	}
	if opt.ScrollID != "" {
		s.ScrollID = opt.ScrollID
	}

	return nil
}
