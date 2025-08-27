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
	StatusPending   = "pending"
	StatusFailed    = "failed"
	StatusCompleted = "completed"
)

type SliceStatusValue struct {
	SliceIdx int `json:"slice_idx"`
	SliceMax int `json:"slice_max"`

	ScrollID  string `json:"scroll_id"`
	Offset    int    `json:"offset"`
	Status    string `json:"status"`
	FailedNum int    `json:"failed_num"`
	Limit     int    `json:"limit"`
}

type ScrollSession struct {
	Ctx               context.Context    `json:"-"`
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

	// 重试次数超过先定之后，直接算失败
	if value.FailedNum > s.SliceMaxFailedNum {
		value.Status = StatusFailed
	}

	s.ScrollIDs[idx] = value
	s.LastAccessAt = time.Now()
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
		ScrollIDs:         make([]SliceStatusValue, maxSlice),
	}

	// 根据 maxSlice 初始化 ScrollIDs
	for idx := 0; idx < maxSlice; idx++ {
		session.ScrollIDs[idx] = SliceStatusValue{
			SliceIdx:  idx,
			SliceMax:  maxSlice,
			ScrollID:  "",
			Offset:    idx * Limit,
			Status:    StatusPending,
			FailedNum: 0,
			Limit:     Limit,
		}
	}

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
