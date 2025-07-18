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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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

// SliceInfo 分片信息
type SliceInfo struct {
	SliceIndex int
	ScrollID   string
	Index      int // 用于 Doris 类型存储
}

// MakeSlices 为指定的 connect 和 tableID 生成所有分片信息
func (s *ScrollSession) MakeSlices(ctx context.Context, storageType, connect, tableID string) ([]SliceInfo, error) {
	switch storageType {
	case consul.ElasticsearchStorageType:
		return s.makeElasticsearchSlices(ctx, connect, tableID)
	case consul.BkSqlStorageType:
		return s.makeDorisSlices(ctx)
	default:
		return nil, fmt.Errorf("unsupported storage type for scroll: %s", storageType)
	}
}

// makeElasticsearchSlices 为 Elasticsearch 生成分片信息
func (s *ScrollSession) makeElasticsearchSlices(ctx context.Context, connect, tableID string) ([]SliceInfo, error) {
	slices := make([]SliceInfo, 0, s.MaxSlice)

	for sliceIndex := 0; sliceIndex < s.MaxSlice; sliceIndex++ {
		scrollID, _, err := s.getNextElasticsearchScrollID(ctx, connect, tableID, sliceIndex)
		if err != nil {
			return nil, fmt.Errorf("failed to get scroll ID for slice %d: %v", sliceIndex, err)
		}

		slices = append(slices, SliceInfo{
			SliceIndex: sliceIndex,
			ScrollID:   scrollID,
			Index:      0, // ES 不使用 index
		})
	}

	return slices, nil
}

// makeDorisSlices 为 Doris 生成分片信息
func (s *ScrollSession) makeDorisSlices(ctx context.Context) ([]SliceInfo, error) {
	slices := make([]SliceInfo, 0, s.MaxSlice)

	for sliceIndex := 0; sliceIndex < s.MaxSlice; sliceIndex++ {
		_, index, err := s.getNextDorisIndex(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get index for slice %d: %v", sliceIndex, err)
		}

		slices = append(slices, SliceInfo{
			SliceIndex: sliceIndex,
			ScrollID:   "", // Doris 不使用 scrollID
			Index:      index,
		})
	}

	return slices, nil
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

// ScrollSessionHelper 滚动查询会话辅助器，简化会话操作
type ScrollSessionHelper struct {
	scrollSliceLimit int
	scrollWindow     string
	scrollMaxSlice   int
	lockTimeout      time.Duration
}

// NewScrollSessionHelper 创建滚动查询会话辅助器
func NewScrollSessionHelper(scrollSliceLimit int, scrollWindow string, scrollMaxSlice int, lockTimeout time.Duration) *ScrollSessionHelper {
	return &ScrollSessionHelper{
		scrollSliceLimit: scrollSliceLimit,
		scrollWindow:     scrollWindow,
		scrollMaxSlice:   scrollMaxSlice,
		lockTimeout:      lockTimeout,
	}
}

func (h *ScrollSessionHelper) GetOrCreateSessionByKey(ctx context.Context, queryTsKey string, clearCache bool,
	scrollWindow string, limit int) (*ScrollSession, bool, error) {

	sessionKey := GetSessionKey(queryTsKey)
	lockKey := GetLockKey(queryTsKey)

	// 获取锁
	scrollWindowDuration, _ := time.ParseDuration(scrollWindow)
	scrollLock, err := ScrollAcquireRedisLock(ctx, lockKey, h.lockTimeout)
	if err != nil {
		return nil, false, fmt.Errorf("failed to acquire lock: %v", err)
	}
	defer func() {
		if releaseErr := ScrollReleaseRedisLock(ctx, scrollLock); releaseErr != nil {
			// 记录错误但不返回，避免影响主要逻辑
		}
	}()

	// 获取或创建会话
	session, err := ScrollGetOrCreateSession(ctx, sessionKey, clearCache, scrollWindowDuration, h.scrollMaxSlice, limit)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get or create session: %v", err)
	}

	isDone := session.Status == SessionStatusDone
	return &session, isDone, nil
}

// ProcessSliceResults 处理分片查询结果
func (h *ScrollSessionHelper) ProcessSliceResults(ctx context.Context, session *ScrollSession, connect, tableId, scrollID string,
	sliceIndex int, storageType string, size int64, options metadata.ResultTableOptions) error {

	return ScrollProcessSliceResult(ctx, session, connect, tableId, scrollID, sliceIndex, storageType, size, options)
}

// UpdateSession 更新会话状态
func (h *ScrollSessionHelper) UpdateSession(ctx context.Context, session *ScrollSession) error {
	queryTsKey := session.Key
	sessionKey := GetSessionKey(queryTsKey)
	return UpdateSession(ctx, sessionKey, *session)
}

// IsSessionDone 检查会话是否完成
func (h *ScrollSessionHelper) IsSessionDone(session *ScrollSession) bool {
	return session.Status == SessionStatusDone
}
