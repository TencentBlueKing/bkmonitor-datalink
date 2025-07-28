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
	LastAccessAt  time.Time                      `json:"last_access_at"`
	ScrollTimeout time.Duration                  `json:"scroll_timeout"`
	MaxSlice      int                            `json:"max_slice"`
	Limit         int                            `json:"limit"`
	Index         int                            `json:"index"`
	ScrollIDs     map[string]map[string]struct{} `json:"scroll_ids"`
	Status        string                         `json:"status"`
}

func NewScrollSession(maxSlice int, scrollTimeout time.Duration, limit int) *ScrollSession {
	return &ScrollSession{
		LastAccessAt:  time.Now(),
		ScrollTimeout: scrollTimeout,
		MaxSlice:      maxSlice,
		Limit:         limit,
		Index:         0,
		ScrollIDs:     map[string]map[string]struct{}{},
		Status:        SessionStatusRunning,
	}
}

type SliceStatus struct {
	Connect  string `json:"connect"`
	TableID  string `json:"table_id"`
	SliceIdx int    `json:"slice_idx"`
}

func (s SliceStatus) String() string {
	return fmt.Sprintf("%s:%s:%d", s.Connect, s.TableID, s.SliceIdx)
}

const (
	SessionStatusRunning = "RUNNING"
	SessionStatusDone    = "DONE"
	SessionStatusFailed  = "FAILED"
)

func (s *ScrollSession) getNextDorisIndex() int {
	currentIndex := s.Index
	s.Index++
	return currentIndex
}

func (s *ScrollSession) getNextElasticsearchScrollID(connect, tableID string, sliceIdx int) string {
	k := SliceStatus{
		Connect:  connect,
		TableID:  tableID,
		SliceIdx: sliceIdx,
	}
	key := k.String()
	scrollIdSet := s.ScrollIDs[key]
	if len(scrollIdSet) == 0 {
		return ""
	}
	for scrollID := range scrollIdSet {
		return scrollID
	}
	return ""
}

func (s *ScrollSession) AddScrollId(connect, tableID, scrollID string, sliceIdx int) {
	k := SliceStatus{
		Connect:  connect,
		TableID:  tableID,
		SliceIdx: sliceIdx,
	}
	key := k.String()

	var idToAdd string
	if scrollID != "" {
		idToAdd = scrollID
		s.ScrollIDs[key] = map[string]struct{}{idToAdd: {}}
	} else {
		if strings.Contains(connect, "bk_data") {
			idToAdd = fmt.Sprintf("slice_%d", sliceIdx)

			st := s.ScrollIDs[key]
			if st == nil {
				st = make(map[string]struct{})
			}

			st[idToAdd] = struct{}{}
			s.ScrollIDs[key] = st
		}
	}
}

func (s *ScrollSession) RemoveScrollID(slice SliceInfo, connect, tableID string, sliceIdx int) {
	k := SliceStatus{
		Connect:  connect,
		TableID:  tableID,
		SliceIdx: sliceIdx,
	}
	key := k.String()
	scrollIDSet := s.ScrollIDs[key]
	if scrollIDSet != nil {
		var idToRemove string
		if slice.ScrollID == "" {
			idToRemove = fmt.Sprintf("slice_%d", sliceIdx)
		} else {
			idToRemove = slice.ScrollID
		}

		delete(scrollIDSet, idToRemove)

		if len(scrollIDSet) == 0 {
			delete(s.ScrollIDs, key)
		}
	}
}

type SliceInfo struct {
	SliceIndex int
	ScrollID   string
	Index      int
}

func (s *ScrollSession) MakeSlices(storageType, connect, tableID string) (sliceInfos []SliceInfo, err error) {
	switch storageType {
	case consul.ElasticsearchStorageType:
		sliceInfos = s.makeElasticsearchSlices(connect, tableID)
		return
	case consul.BkSqlStorageType:
		sliceInfos = s.makeDorisSlices()
		return
	default:
		err = fmt.Errorf("unsupported storage type for scroll: %s", storageType)
		return
	}
}

func (s *ScrollSession) makeElasticsearchSlices(connect, tableID string) []SliceInfo {
	slices := make([]SliceInfo, 0, s.MaxSlice)

	isFirstRequest := len(s.ScrollIDs) == 0

	for sliceIndex := 0; sliceIndex < s.MaxSlice; sliceIndex++ {
		scrollID := s.getNextElasticsearchScrollID(connect, tableID, sliceIndex)

		if isFirstRequest || scrollID != "" {
			slices = append(slices, SliceInfo{
				SliceIndex: sliceIndex,
				ScrollID:   scrollID,
				Index:      0,
			})
		}
	}

	return slices
}

func (s *ScrollSession) makeDorisSlices() []SliceInfo {
	slices := make([]SliceInfo, 0, s.MaxSlice)

	roundNumber := 0
	if len(s.ScrollIDs) > 0 {
		hasActiveSlices := false
		for _, scrollIDSet := range s.ScrollIDs {
			if len(scrollIDSet) > 0 {
				hasActiveSlices = true
				break
			}
		}
		if hasActiveSlices {
			roundNumber = s.Index + 1
		}
	}

	for sliceIndex := 0; sliceIndex < s.MaxSlice; sliceIndex++ {
		offsetIndex := roundNumber*s.MaxSlice + sliceIndex
		slices = append(slices, SliceInfo{
			SliceIndex: sliceIndex,
			ScrollID:   "",
			Index:      offsetIndex,
		})
	}

	s.Index = roundNumber

	return slices
}

func (s *ScrollSession) HasMoreData(tsDbType string) bool {
	switch tsDbType {
	case consul.ElasticsearchStorageType:
		for _, scrollIDSet := range s.ScrollIDs {
			if len(scrollIDSet) > 0 {
				for scrollID := range scrollIDSet {
					if scrollID != "" {
						return true
					}
				}
			}
		}
		return false
	case consul.BkSqlStorageType:
		for _, scrollIDSet := range s.ScrollIDs {
			if len(scrollIDSet) > 0 {
				return true
			}
		}
		return false
	default:
		return false
	}
}

type ScrollSessionHelper struct {
	scrollSliceLimit int
	scrollWindow     string
	scrollMaxSlice   int
	lockTimeout      time.Duration
}

func NewScrollSessionHelper(scrollSliceLimit int, scrollWindow string, scrollMaxSlice int, lockTimeout time.Duration) *ScrollSessionHelper {
	return &ScrollSessionHelper{
		scrollSliceLimit: scrollSliceLimit,
		scrollWindow:     scrollWindow,
		scrollMaxSlice:   scrollMaxSlice,
		lockTimeout:      lockTimeout,
	}
}

func (h *ScrollSessionHelper) GetOrCreateSessionByKey(ctx context.Context, queryTsKey string, clearCache bool,
	scrollWindow string, limit int) (*ScrollSession, string, bool, error) {
	sessionKey := GetSessionKey(queryTsKey)

	scrollWindowDuration, err := time.ParseDuration(scrollWindow)
	if err != nil {
		return nil, "", false, err
	}

	session, err := ScrollGetOrCreateSession(ctx, sessionKey, clearCache, scrollWindowDuration, h.scrollMaxSlice, limit)
	if err != nil {
		return nil, "", false, err
	}

	isDone := session.Status == SessionStatusDone
	return session, sessionKey, isDone, nil
}
