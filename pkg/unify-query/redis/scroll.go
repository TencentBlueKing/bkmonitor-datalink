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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	SessionKeyPrefix = "scroll:session:"
	LockKeyPrefix    = "scroll:lock:"
)

// ScrollSession 用于记录当前scroll的一些meta信息
// 对于ES：管理scrollID列表，按照maxSlice并发处理
// 对于Doris：管理index，按照指定数目检索数据
type ScrollSession struct {
	Key           string
	CreateAt      time.Time     `json:"create_at"`
	LastAccessAt  time.Time     `json:"last_access_at"`
	ScrollTimeout time.Duration `json:"scroll_timeout"`
	MaxSlice      int           `json:"max_slice"`
	Limit         int           `json:"limit"`
	Index         int           `json:"index"`
	// key: connect|tableID, value: scrollID列表(ES) 或 空(Doris)
	ScrollIDs map[string][]string `json:"scroll_ids"`
	// key: connect|tableID|scrollID, value: 是否已完成
	ScrollIDStatus map[string]bool `json:"scroll_id_status"`
	Status         string          `json:"status"`
}

const (
	SessionStatusRunning = "RUNNING"
	SessionStatusDone    = "DONE"
	SessionStatusFailed  = "FAILED"
)

// GetNextScrollID 获取下一个可用的scrollID(ES)或返回当前index(Doris)
func (s *ScrollSession) GetNextScrollID(ctx context.Context, tsDbType, connect, tableID string, sliceIdx int) (string, int, error) {
	if tsDbType == "elasticsearch" {
		mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
		log.Debugf(ctx, "[DEBUG] GetNextScrollID: mapKey=%s", mapKey)
		log.Debugf(ctx, "[DEBUG] GetNextScrollID: all ScrollIDs=%+v", s.ScrollIDs)
		log.Debugf(ctx, "[DEBUG] GetNextScrollID: all ScrollIDStatus=%+v", s.ScrollIDStatus)

		// ES场景：获取该slice对应的scrollID
		scrollIDs, exist := s.ScrollIDs[mapKey]
		if !exist || len(scrollIDs) == 0 {
			// 没有可用的scrollID，返回空字符串用于创建新的scroll
			log.Debugf(ctx, "[DEBUG] GetNextScrollID: no scrollIDs for %s, returning empty", mapKey)
			return "", 0, nil
		}

		log.Debugf(ctx, "[DEBUG] GetNextScrollID: found %d scrollIDs for %s: %v", len(scrollIDs), mapKey, scrollIDs)

		// 初始化ScrollIDStatus
		if s.ScrollIDStatus == nil {
			s.ScrollIDStatus = make(map[string]bool)
		}

		// 查找未完成的scrollID
		for i := 0; i < len(scrollIDs); i++ {
			scrollID := scrollIDs[i]
			statusKey := fmt.Sprintf("%s|%s", mapKey, scrollID)

			// 如果scrollID未标记为完成，则使用它
			if !s.ScrollIDStatus[statusKey] {
				log.Debugf(ctx, "[DEBUG] GetNextScrollID: found active scrollID %s for %s", scrollID, mapKey)
				return scrollID, 0, nil
			} else {
				log.Debugf(ctx, "[DEBUG] GetNextScrollID: skipping completed scrollID %s for %s", scrollID, mapKey)
			}
		}

		// 所有scrollID都已完成
		log.Debugf(ctx, "[DEBUG] GetNextScrollID: all scrollIDs completed for %s", mapKey)
		return "", 0, nil
	} else {
		// Doris场景：使用index计算偏移量
		currentIndex := s.Index
		s.Index++
		log.Debugf(ctx, "[DEBUG] GetNextScrollID: Doris mode, returning index %d", currentIndex)

		return "", currentIndex, nil
	}
}

// AddScrollID 添加新的scrollID到列表中(仅ES使用)
func (s *ScrollSession) AddScrollID(connect, tableID, scrollID string, sliceIdx int) {
	if s.ScrollIDs == nil {
		s.ScrollIDs = make(map[string][]string)
	}

	mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
	s.ScrollIDs[mapKey] = append(s.ScrollIDs[mapKey], scrollID)

	// 添加调试信息
	log.Debugf(context.Background(), "[DEBUG] AddScrollID: mapKey=%s, scrollID=%s, total scrollIDs for this slice: %d", mapKey, scrollID, len(s.ScrollIDs[mapKey]))
	log.Debugf(context.Background(), "[DEBUG] AddScrollID: current ScrollIDs for slice: %v", s.ScrollIDs[mapKey])
	log.Debugf(context.Background(), "[DEBUG] AddScrollID: all ScrollIDs: %+v", s.ScrollIDs)
}

// MarkScrollIDDone 标记scrollID为完成状态(仅ES使用)
func (s *ScrollSession) MarkScrollIDDone(connect, tableID, scrollID string, sliceIdx int) {
	if s.ScrollIDStatus == nil {
		s.ScrollIDStatus = make(map[string]bool)
	}

	mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
	statusKey := fmt.Sprintf("%s|%s", mapKey, scrollID)
	s.ScrollIDStatus[statusKey] = true

	log.Debugf(context.Background(), "[DEBUG] MarkScrollIDDone: marked %s as done", statusKey)
	log.Debugf(context.Background(), "[DEBUG] MarkScrollIDDone: all ScrollIDStatus: %+v", s.ScrollIDStatus)
}

// RemoveScrollID 从列表中移除指定的scrollID(仅ES使用)
func (s *ScrollSession) RemoveScrollID(connect, tableID, scrollID string, sliceIdx int) {
	mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
	scrollIDs, exist := s.ScrollIDs[mapKey]
	if !exist {
		log.Debugf(context.Background(), "[DEBUG] RemoveScrollID: no scrollIDs found for %s", mapKey)
		return
	}

	// 过滤掉指定的scrollID
	newScrollIDs := make([]string, 0, len(scrollIDs))
	for _, id := range scrollIDs {
		if id != scrollID {
			newScrollIDs = append(newScrollIDs, id)
		}
	}
	s.ScrollIDs[mapKey] = newScrollIDs
	log.Debugf(context.Background(), "[DEBUG] RemoveScrollID: removed %s from %s, remaining: %v", scrollID, mapKey, newScrollIDs)
}

// HasMoreData 检查是否还有更多数据需要处理
// ES: 通过检查是否还有未完成的scrollID来判断
// Doris: 通过检查是否遇到了空结果来判断
func (s *ScrollSession) HasMoreData(tsDbType string) bool {
	if s.ScrollIDs == nil {
		log.Debugf(context.Background(), "[DEBUG] HasMoreData: ScrollIDs is nil, returning false")
		return false
	}

	if tsDbType == "elasticsearch" {
		// ES场景：检查是否还有未完成的scrollID
		if s.ScrollIDStatus == nil {
			s.ScrollIDStatus = make(map[string]bool)
		}

		log.Debugf(context.Background(), "[DEBUG] HasMoreData: checking ES scrollIDs")
		log.Debugf(context.Background(), "[DEBUG] HasMoreData: all ScrollIDs: %+v", s.ScrollIDs)
		log.Debugf(context.Background(), "[DEBUG] HasMoreData: all ScrollIDStatus: %+v", s.ScrollIDStatus)

		for mapKey, scrollIDs := range s.ScrollIDs {
			log.Debugf(context.Background(), "[DEBUG] HasMoreData: checking mapKey=%s with %d scrollIDs: %v", mapKey, len(scrollIDs), scrollIDs)
			for _, scrollID := range scrollIDs {
				// 确保statusKey格式与MarkScrollIDDone一致
				statusKey := fmt.Sprintf("%s|%s", mapKey, scrollID)
				isDone := s.ScrollIDStatus[statusKey]
				log.Debugf(context.Background(), "[DEBUG] HasMoreData: scrollID=%s, statusKey=%s, isDone=%t", scrollID, statusKey, isDone)
				if !isDone {
					log.Debugf(context.Background(), "[DEBUG] HasMoreData: found active scrollID %s, returning true", scrollID)
					return true
				}
			}
		}
		log.Debugf(context.Background(), "[DEBUG] HasMoreData: all ES scrollIDs are done, returning false")
		return false
	} else {
		// Doris场景：如果Status不是Done，说明还有数据
		// 当遇到空结果时，会在ScrollProcessSliceResult中设置为Done
		hasMore := s.Status != SessionStatusDone
		log.Debugf(context.Background(), "[DEBUG] HasMoreData: Doris mode, status=%s, hasMore=%t", s.Status, hasMore)
		return hasMore
	}
}
