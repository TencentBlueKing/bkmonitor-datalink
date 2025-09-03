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
	StatusFailed    = "failed"
	StatusCompleted = "completed"
)

type SliceStatusValue struct {
	SliceIdx int `json:"slice_idx"`
	SliceMax int `json:"slice_max"`

	ScrollID     string `json:"scroll_id"`
	Offset       int    `json:"offset"`
	Status       string `json:"status"`
	FailedNum    int    `json:"failed_num"`
	MaxFailedNum int    `json:"max_failed_num"`
	Limit        int    `json:"limit"`
}

func (s *SliceStatusValue) Done() bool {
	return s.Status == StatusCompleted || s.Status == StatusFailed
}

type ScrollSession struct {
	SessionKey          string             `json:"session_key"`
	LockKey             string             `json:"lock_key"`
	LastAccessAt        time.Time          `json:"last_access_at"`
	ScrollWindowTimeout time.Duration      `json:"scroll_window_timeout"`
	ScrollLockTimeout   time.Duration      `json:"scroll_lock_timeout"`
	MaxSlice            int                `json:"max_slice"`
	SliceMaxFailedNum   int                `json:"slice_max_failed_num"`
	Limit               int                `json:"limit"`
	ScrollIDs           []SliceStatusValue `json:"scroll_ids"`

	mu sync.RWMutex
}

func (s *ScrollSession) UpdateSliceStatus(idx int, value SliceStatusValue) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 重试次数超过先定之后，直接算失败
	if value.FailedNum > s.SliceMaxFailedNum {
		value.Status = StatusFailed
	}

	s.ScrollIDs[idx] = value
	s.LastAccessAt = time.Now()
}

func (s *ScrollSession) AcquireLock(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := Client().SetNX(ctx, s.LockKey, "locked", s.ScrollLockTimeout).Err()
	if err != nil {
		return errors.Wrap(err, "failed to acquire lock")
	}

	s.LastAccessAt = time.Now()
	return Client().Set(ctx, s.SessionKey, s, s.ScrollLockTimeout).Err()
}

func (s *ScrollSession) ReleaseLock(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := Client().Del(ctx, s.LockKey).Err()
	if err != nil {
		return err
	}
	s.LastAccessAt = time.Now()
	return Client().Set(ctx, s.SessionKey, s, s.ScrollWindowTimeout).Err()
}

func (s *ScrollSession) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

func (s *ScrollSession) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, s)
}

func (s *ScrollSession) Done() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sliceValue := range s.ScrollIDs {
		if sliceValue.Status != StatusCompleted && sliceValue.FailedNum < s.SliceMaxFailedNum {
			return false
		}
	}
	return true
}

func newScrollSession(queryTsStr string, scrollTimeout, scrollLockTimeout time.Duration, maxSlice, sliceMaxFailedNum, Limit int) *ScrollSession {
	session := &ScrollSession{
		SessionKey:          SessionKeyPrefix + queryTsStr,
		LockKey:             ScrollLockKeyPrefix + queryTsStr,
		LastAccessAt:        time.Now(),
		ScrollWindowTimeout: scrollTimeout,
		ScrollLockTimeout:   scrollLockTimeout,
		MaxSlice:            maxSlice,
		SliceMaxFailedNum:   sliceMaxFailedNum,
		Limit:               Limit,
		ScrollIDs:           make([]SliceStatusValue, maxSlice),
	}

	// 根据 maxSlice 初始化 ScrollIDs
	for idx := 0; idx < maxSlice; idx++ {
		session.ScrollIDs[idx] = SliceStatusValue{
			SliceIdx:     idx,
			SliceMax:     maxSlice,
			ScrollID:     "",
			Offset:       idx * Limit,
			Status:       StatusPending,
			FailedNum:    0,
			MaxFailedNum: sliceMaxFailedNum,
			Limit:        Limit,
		}
	}

	return session
}

func GetOrCreateScrollSession(ctx context.Context, queryTsStr string, scrollWindowTimeout, scrollLockTimeout string, maxSlice, Limit int) (session *ScrollSession, err error) {
	session, exist := checkScrollSession(ctx, queryTsStr)
	if exist {
		return
	}
	scrollWindowTimeoutDuration, err := time.ParseDuration(scrollWindowTimeout)
	if err != nil {
		return
	}
	scrollLockTimeoutDuration, err := time.ParseDuration(scrollLockTimeout)
	if err != nil {
		return
	}

	session = newScrollSession(queryTsStr, scrollWindowTimeoutDuration, scrollLockTimeoutDuration, maxSlice, DefaultSliceMaxFailedNum, Limit)
	err = Client().SetNX(ctx, session.SessionKey, session, scrollWindowTimeoutDuration).Err()
	if err != nil {
		return
	}
	return
}

func checkScrollSession(ctx context.Context, queryTsStr string) (*ScrollSession, bool) {
	session := &ScrollSession{}
	err := Client().Get(ctx, SessionKeyPrefix+queryTsStr).Scan(session)
	if err != nil {
		return nil, false
	}

	return session, true
}
