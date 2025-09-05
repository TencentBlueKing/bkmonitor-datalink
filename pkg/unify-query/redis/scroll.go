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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
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

type SliceStatus struct {
	SliceKey     string `json:"slice_key"`
	SliceMax     int    `json:"slice_max"`
	ScrollID     string `json:"scroll_id"`
	Offset       int    `json:"offset"`
	Status       string `json:"status"`
	FailedNum    int    `json:"failed_num"`
	MaxFailedNum int    `json:"max_failed_num"`
	Limit        int    `json:"limit"`
}

func (s *SliceStatus) Done() bool {
	return s.Status == StatusCompleted || s.Status == StatusFailed
}

type ScrollSession struct {
	SessionKey          string        `json:"session_key"`
	LockKey             string        `json:"lock_key"`
	LastAccessAt        time.Time     `json:"-"`
	ScrollWindowTimeout time.Duration `json:"scroll_window_timeout"`
	ScrollLockTimeout   time.Duration `json:"scroll_lock_timeout"`
	MaxSlice            int           `json:"max_slice"`
	SliceMaxFailedNum   int           `json:"slice_max_failed_num"`
	Limit               int           `json:"limit"`

	SlicesMap map[string]*SliceStatus `json:"slices_map"`

	mu sync.RWMutex
}

func (s *ScrollSession) Slice(key string) *SliceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if slice, ok := s.SlicesMap[key]; ok {
		return slice
	}

	return &SliceStatus{
		SliceKey:     key,
		SliceMax:     s.MaxSlice,
		MaxFailedNum: s.SliceMaxFailedNum,
		Limit:        s.Limit,
		Status:       StatusPending,
	}
}

func (s *ScrollSession) SliceLength() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.MaxSlice < 2 {
		return 1
	}

	return s.MaxSlice
}

func (s *ScrollSession) UpdateSliceStatus(key string, value *SliceStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 重试次数超过先定之后，直接算失败
	if value.FailedNum > s.SliceMaxFailedNum {
		value.Status = StatusFailed
	}

	s.SlicesMap[key] = value
	s.LastAccessAt = time.Now()
}

func (s *ScrollSession) Lock(ctx context.Context) error {
	return Client().SetNX(ctx, s.LockKey, "locked", s.ScrollLockTimeout).Err()
}

func (s *ScrollSession) UnLock(ctx context.Context) error {
	return Client().Del(ctx, s.LockKey).Err()
}

func (s *ScrollSession) Update(ctx context.Context) error {
	return Client().Set(ctx, s.SessionKey, s, s.ScrollWindowTimeout).Err()
}

func (s *ScrollSession) Clear(ctx context.Context) error {
	return Client().Del(ctx, s.SessionKey).Err()
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

	if len(s.SlicesMap) == 0 {
		return false
	}

	for _, sliceValue := range s.SlicesMap {
		if sliceValue.Status != StatusCompleted && sliceValue.FailedNum < s.SliceMaxFailedNum {
			return false
		}
	}
	return true
}

func NewScrollSession(queryTsStr string, scrollTimeout, scrollLockTimeout time.Duration, maxSlice, sliceMaxFailedNum, limit int) *ScrollSession {
	session := &ScrollSession{
		SessionKey:          SessionKeyPrefix + queryTsStr,
		LockKey:             ScrollLockKeyPrefix + queryTsStr,
		LastAccessAt:        time.Now(),
		ScrollWindowTimeout: scrollTimeout,
		ScrollLockTimeout:   scrollLockTimeout,
		MaxSlice:            maxSlice,
		SliceMaxFailedNum:   sliceMaxFailedNum,
		Limit:               limit,
		SlicesMap:           make(map[string]*SliceStatus),
	}

	return session
}

func GetOrCreateScrollSession(ctx context.Context, queryTsStr string, scrollWindowTimeout, scrollLockTimeout string, maxSlice, Limit int) (*ScrollSession, error) {
	scrollWindowTimeoutDuration, err := time.ParseDuration(scrollWindowTimeout)
	if err != nil {
		return nil, err
	}
	scrollLockTimeoutDuration, err := time.ParseDuration(scrollLockTimeout)
	if err != nil {
		return nil, err
	}

	session := NewScrollSession(queryTsStr, scrollWindowTimeoutDuration, scrollLockTimeoutDuration, maxSlice, DefaultSliceMaxFailedNum, Limit)
	if sessionCache, ok := checkScrollSession(ctx, session.SessionKey); ok {
		log.Debugf(ctx, "session cache")
		return sessionCache, nil
	}

	// set session cache
	err = Client().SetNX(ctx, session.SessionKey, session, scrollWindowTimeoutDuration).Err()
	if err != nil {
		return nil, err
	}

	log.Debugf(ctx, "session new")
	return session, nil
}

func checkScrollSession(ctx context.Context, key string) (*ScrollSession, bool) {
	session := &ScrollSession{}
	res := Client().Get(ctx, key).Val()
	if res != "" {
		err := json.Unmarshal([]byte(res), &session)
		if err == nil {
			return session, true
		}
	}
	return nil, false
}
