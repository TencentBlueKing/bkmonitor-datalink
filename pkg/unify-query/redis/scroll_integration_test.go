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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// TestCompleteESScrollFlow 测试完整的ES scroll流程
func TestCompleteESScrollFlow(t *testing.T) {
	ctx := context.Background()

	// 创建session
	session := ScrollSession{
		Key:            "test_session_complete_flow",
		CreateAt:       time.Now(),
		LastAccessAt:   time.Now(),
		Index:          0,
		MaxSlice:       3,
		Limit:          10,
		ScrollTimeout:  5 * time.Minute,
		Status:         SessionStatusRunning,
		ScrollIDs:      make(map[string][]string),
		ScrollIDStatus: make(map[string]bool),
	}

	connect := "http://127.0.0.1:9200"
	tableID := "test_table"

	log.Infof(ctx, "[TEST] ====== 开始ES scroll完整流程测试 ======")

	// 模拟第一轮查询：每个slice都获取到新的scrollID
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		// 第一次查询，应该返回空scrollID（用于创建新的scroll）
		scrollID, index, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
		assert.NoError(t, err)
		assert.Equal(t, "", scrollID, "First query should return empty scrollID")
		assert.Equal(t, 0, index)

		// 模拟ES返回新的scrollID
		newScrollID := fmt.Sprintf("scroll_id_%d_round1", sliceIdx)

		// 创建options模拟ES返回的结果
		options := make(metadata.ResultTableOptions)
		options.SetOption(tableID, connect, &metadata.ResultTableOption{
			ScrollID: newScrollID,
		})

		// 处理slice结果，应该添加新的scrollID
		err = ScrollProcessSliceResult(ctx, &session, connect, tableID, "", sliceIdx, "elasticsearch", 5, options)
		assert.NoError(t, err)

		log.Infof(ctx, "[TEST] Slice %d round 1: added scrollID %s", sliceIdx, newScrollID)
	}

	// 验证session状态
	assert.Equal(t, SessionStatusRunning, session.Status, "Session should still be running")
	assert.True(t, session.HasMoreData("elasticsearch"), "Should have more data")

	log.Infof(ctx, "[TEST] ====== 第二轮查询 ======")

	// 第二轮查询：使用已有的scrollID
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		// 应该返回第一轮创建的scrollID
		scrollID, index, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("scroll_id_%d_round1", sliceIdx), scrollID)
		assert.Equal(t, 0, index)

		// 模拟ES返回新的scrollID
		newScrollID := fmt.Sprintf("scroll_id_%d_round2", sliceIdx)

		// 创建options模拟ES返回的结果
		options := make(metadata.ResultTableOptions)
		options.SetOption(tableID, connect, &metadata.ResultTableOption{
			ScrollID: newScrollID,
		})

		// 处理slice结果，应该添加新的scrollID
		err = ScrollProcessSliceResult(ctx, &session, connect, tableID, scrollID, sliceIdx, "elasticsearch", 3, options)
		assert.NoError(t, err)

		log.Infof(ctx, "[TEST] Slice %d round 2: using scrollID %s, got new scrollID %s", sliceIdx, scrollID, newScrollID)
	}

	// 验证session状态
	assert.Equal(t, SessionStatusRunning, session.Status, "Session should still be running")
	assert.True(t, session.HasMoreData("elasticsearch"), "Should have more data")

	log.Infof(ctx, "[TEST] ====== 第三轮查询（最后一轮） ======")

	// 第三轮查询：ES返回空结果，结束scroll
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		// 应该返回第二轮创建的scrollID
		scrollID, index, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("scroll_id_%d_round2", sliceIdx), scrollID)
		assert.Equal(t, 0, index)

		// 模拟ES返回空结果（没有新的scrollID）
		options := make(metadata.ResultTableOptions)
		options.SetOption(tableID, connect, &metadata.ResultTableOption{
			ScrollID: "", // 空的scrollID表示结束
		})

		// 处理slice结果，size=0表示没有更多数据
		err = ScrollProcessSliceResult(ctx, &session, connect, tableID, scrollID, sliceIdx, "elasticsearch", 0, options)
		assert.NoError(t, err)

		// 手动标记scrollID为完成
		session.MarkScrollIDDone(connect, tableID, scrollID, sliceIdx)

		log.Infof(ctx, "[TEST] Slice %d round 3: using scrollID %s, got empty result", sliceIdx, scrollID)
	}

	// 验证session状态
	assert.False(t, session.HasMoreData("elasticsearch"), "Should not have more data")
	assert.Equal(t, SessionStatusDone, session.Status, "Session should be done")

	log.Infof(ctx, "[TEST] ====== 测试完成 ======")
}

// TestESScrollProblematicFlow 测试有问题的ES scroll流程
func TestESScrollProblematicFlow(t *testing.T) {
	ctx := context.Background()

	// 创建session
	session := ScrollSession{
		Key:            "test_session_problematic",
		CreateAt:       time.Now(),
		LastAccessAt:   time.Now(),
		Index:          0,
		MaxSlice:       3,
		Limit:          10,
		ScrollTimeout:  5 * time.Minute,
		Status:         SessionStatusRunning,
		ScrollIDs:      make(map[string][]string),
		ScrollIDStatus: make(map[string]bool),
	}

	connect := "http://127.0.0.1:9200"
	tableID := "test_table"

	log.Infof(ctx, "[TEST] ====== 测试有问题的ES scroll流程 ======")

	// 模拟第一轮查询：每个slice都获取到新的scrollID
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		scrollID, _, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
		assert.NoError(t, err)
		assert.Equal(t, "", scrollID, "First query should return empty scrollID")

		// 模拟ES返回新的scrollID
		newScrollID := fmt.Sprintf("scroll_id_%d_round1", sliceIdx)

		options := make(metadata.ResultTableOptions)
		options.SetOption(tableID, connect, &metadata.ResultTableOption{
			ScrollID: newScrollID,
		})

		err = ScrollProcessSliceResult(ctx, &session, connect, tableID, "", sliceIdx, "elasticsearch", 5, options)
		assert.NoError(t, err)

		log.Infof(ctx, "[TEST] Slice %d round 1: added scrollID %s", sliceIdx, newScrollID)
	}

	// 验证第一轮后的状态
	assert.Equal(t, SessionStatusRunning, session.Status)
	assert.True(t, session.HasMoreData("elasticsearch"))

	log.Infof(ctx, "[TEST] Session ScrollIDs after round 1: %+v", session.ScrollIDs)

	// 模拟第二轮查询：每个slice又获取到新的scrollID（这里可能是问题所在）
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		scrollID, _, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("scroll_id_%d_round1", sliceIdx), scrollID)

		// 模拟ES返回新的scrollID（不断产生新的scrollID）
		newScrollID := fmt.Sprintf("scroll_id_%d_round2", sliceIdx)

		options := make(metadata.ResultTableOptions)
		options.SetOption(tableID, connect, &metadata.ResultTableOption{
			ScrollID: newScrollID,
		})

		err = ScrollProcessSliceResult(ctx, &session, connect, tableID, scrollID, sliceIdx, "elasticsearch", 3, options)
		assert.NoError(t, err)

		log.Infof(ctx, "[TEST] Slice %d round 2: using scrollID %s, got new scrollID %s", sliceIdx, scrollID, newScrollID)
	}

	log.Infof(ctx, "[TEST] Session ScrollIDs after round 2: %+v", session.ScrollIDs)
	log.Infof(ctx, "[TEST] Session ScrollIDStatus after round 2: %+v", session.ScrollIDStatus)

	// 验证第二轮后的状态
	assert.Equal(t, SessionStatusRunning, session.Status)
	assert.True(t, session.HasMoreData("elasticsearch"))

	// 检查每个slice的scrollID数量
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
		scrollIDs := session.ScrollIDs[mapKey]
		log.Infof(ctx, "[TEST] Slice %d has %d scrollIDs: %v", sliceIdx, len(scrollIDs), scrollIDs)
		assert.Equal(t, 2, len(scrollIDs), "Each slice should have 2 scrollIDs")
	}

	// 模拟第三轮查询：继续产生新的scrollID（无限循环问题）
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		scrollID, _, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("scroll_id_%d_round2", sliceIdx), scrollID)

		// 模拟ES继续返回新的scrollID（问题：无限产生新的scrollID）
		newScrollID := fmt.Sprintf("scroll_id_%d_round3", sliceIdx)

		options := make(metadata.ResultTableOptions)
		options.SetOption(tableID, connect, &metadata.ResultTableOption{
			ScrollID: newScrollID,
		})

		err = ScrollProcessSliceResult(ctx, &session, connect, tableID, scrollID, sliceIdx, "elasticsearch", 2, options)
		assert.NoError(t, err)

		log.Infof(ctx, "[TEST] Slice %d round 3: using scrollID %s, got new scrollID %s", sliceIdx, scrollID, newScrollID)
	}

	log.Infof(ctx, "[TEST] Session ScrollIDs after round 3: %+v", session.ScrollIDs)

	// 验证第三轮后的状态 - 这里展示了问题：session永远不会结束
	assert.Equal(t, SessionStatusRunning, session.Status, "Session keeps running indefinitely")
	assert.True(t, session.HasMoreData("elasticsearch"), "Always has more data")

	// 检查每个slice的scrollID数量 - 不断增长
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
		scrollIDs := session.ScrollIDs[mapKey]
		log.Infof(ctx, "[TEST] Slice %d now has %d scrollIDs: %v", sliceIdx, len(scrollIDs), scrollIDs)
		assert.Equal(t, 3, len(scrollIDs), "Each slice now has 3 scrollIDs")
	}

	log.Infof(ctx, "[TEST] ====== 问题流程测试完成 - 展示了无限循环问题 ======")
}

// TestESScrollCorrectTermination 测试正确的ES scroll终止逻辑
func TestESScrollCorrectTermination(t *testing.T) {
	ctx := context.Background()

	// 创建session
	session := ScrollSession{
		Key:            "test_session_correct_termination",
		CreateAt:       time.Now(),
		LastAccessAt:   time.Now(),
		Index:          0,
		MaxSlice:       3,
		Limit:          10,
		ScrollTimeout:  5 * time.Minute,
		Status:         SessionStatusRunning,
		ScrollIDs:      make(map[string][]string),
		ScrollIDStatus: make(map[string]bool),
	}

	connect := "http://127.0.0.1:9200"
	tableID := "test_table"

	log.Infof(ctx, "[TEST] ====== 测试正确的ES scroll终止逻辑 ======")

	// 第一轮查询
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		scrollID, _, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
		assert.NoError(t, err)
		assert.Equal(t, "", scrollID)

		newScrollID := fmt.Sprintf("scroll_id_%d_round1", sliceIdx)

		options := make(metadata.ResultTableOptions)
		options.SetOption(tableID, connect, &metadata.ResultTableOption{
			ScrollID: newScrollID,
		})

		err = ScrollProcessSliceResult(ctx, &session, connect, tableID, "", sliceIdx, "elasticsearch", 5, options)
		assert.NoError(t, err)
	}

	// 第二轮查询：模拟ES返回空结果和空scrollID
	for sliceIdx := 0; sliceIdx < 3; sliceIdx++ {
		scrollID, _, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("scroll_id_%d_round1", sliceIdx), scrollID)

		// 模拟ES返回空结果和空scrollID（正确的终止条件）
		options := make(metadata.ResultTableOptions)
		options.SetOption(tableID, connect, &metadata.ResultTableOption{
			ScrollID: "", // 空scrollID表示scroll结束
		})

		err = ScrollProcessSliceResult(ctx, &session, connect, tableID, scrollID, sliceIdx, "elasticsearch", 0, options)
		assert.NoError(t, err)

		// 关键：手动标记scrollID为完成
		session.MarkScrollIDDone(connect, tableID, scrollID, sliceIdx)

		log.Infof(ctx, "[TEST] Slice %d terminated: scrollID %s marked as done", sliceIdx, scrollID)
	}

	// 验证session正确终止
	assert.False(t, session.HasMoreData("elasticsearch"), "Should not have more data")
	assert.Equal(t, SessionStatusDone, session.Status, "Session should be done")

	log.Infof(ctx, "[TEST] ====== 正确终止测试完成 ======")
}

// TestRealESScrollFlow 测试真实的ES scroll流程
func TestRealESScrollFlow(t *testing.T) {
	ctx := context.Background()

	// 创建session
	session := ScrollSession{
		Key:            "test_real_es_scroll_flow",
		CreateAt:       time.Now(),
		LastAccessAt:   time.Now(),
		Index:          0,
		MaxSlice:       3,
		Limit:          10,
		ScrollTimeout:  5 * time.Minute,
		Status:         SessionStatusRunning,
		ScrollIDs:      make(map[string][]string),
		ScrollIDStatus: make(map[string]bool),
	}

	connect := "http://127.0.0.1:9200"
	tableID := "test_table"

	log.Infof(ctx, "[TEST] ====== 测试真实的ES scroll流程 ======")

	// 模拟多轮查询，直到所有数据获取完毕
	maxRounds := 10 // 防止无限循环
	for round := 1; round <= maxRounds; round++ {
		log.Infof(ctx, "[TEST] ====== 第 %d 轮查询 ======", round)

		// 对每个slice进行查询
		for sliceIdx := 0; sliceIdx < session.MaxSlice; sliceIdx++ {
			// 获取scrollID
			scrollID, _, err := session.GetNextScrollID(ctx, "elasticsearch", connect, tableID, sliceIdx)
			assert.NoError(t, err)

			log.Infof(ctx, "[TEST] Round %d, Slice %d: scrollID=%s", round, sliceIdx, scrollID)

			// 如果没有可用的scrollID，跳过这个slice
			if scrollID == "" {
				// 检查是否已经有scrollID存在
				mapKey := fmt.Sprintf("%s|%s|%d", connect, tableID, sliceIdx)
				if existingScrollIDs, exists := session.ScrollIDs[mapKey]; exists && len(existingScrollIDs) > 0 {
					log.Infof(ctx, "[TEST] Round %d, Slice %d: no more active scrollIDs, slice completed", round, sliceIdx)
					continue
				}

				log.Infof(ctx, "[TEST] Round %d, Slice %d: starting new scroll", round, sliceIdx)
			}

			// 模拟ES查询结果
			var size int64
			var newScrollID string

			if round <= 3 {
				// 前3轮返回数据
				size = int64(5 - round) // 数据量递减
				if size > 0 {
					newScrollID = fmt.Sprintf("scroll_id_%d_round%d", sliceIdx, round+1)
				}
			} else {
				// 第4轮及以后返回空数据
				size = 0
				newScrollID = "" // 空scrollID表示结束
			}

			// 创建options
			options := make(metadata.ResultTableOptions)
			options.SetOption(tableID, connect, &metadata.ResultTableOption{
				ScrollID: newScrollID,
			})

			// 处理slice结果
			err = ScrollProcessSliceResult(ctx, &session, connect, tableID, scrollID, sliceIdx, "elasticsearch", size, options)
			assert.NoError(t, err)

			log.Infof(ctx, "[TEST] Round %d, Slice %d: processed result, size=%d, newScrollID=%s", round, sliceIdx, size, newScrollID)
		}

		// 检查session状态
		hasMoreData := session.HasMoreData("elasticsearch")
		log.Infof(ctx, "[TEST] Round %d: hasMoreData=%t, sessionStatus=%s", round, hasMoreData, session.Status)

		// 如果所有slice都完成了，或者session状态为Done，结束循环
		if !hasMoreData || session.Status == SessionStatusDone {
			log.Infof(ctx, "[TEST] Round %d: all scrolls completed, breaking", round)
			break
		}

		// 验证session状态
		if round <= 3 {
			assert.Equal(t, SessionStatusRunning, session.Status, "Session should be running in round %d", round)
			assert.True(t, hasMoreData, "Should have more data in round %d", round)
		}
	}

	// 最终验证
	assert.False(t, session.HasMoreData("elasticsearch"), "Should not have more data at the end")
	assert.Equal(t, SessionStatusDone, session.Status, "Session should be done at the end")

	// 验证所有scrollID都被标记为完成
	for mapKey, scrollIDs := range session.ScrollIDs {
		for _, scrollID := range scrollIDs {
			statusKey := fmt.Sprintf("%s|%s", mapKey, scrollID)
			assert.True(t, session.ScrollIDStatus[statusKey], "ScrollID %s should be marked as done", scrollID)
		}
	}

	log.Infof(ctx, "[TEST] ====== 真实ES scroll流程测试完成 ======")
	log.Infof(ctx, "[TEST] Final session ScrollIDs: %+v", session.ScrollIDs)
	log.Infof(ctx, "[TEST] Final session ScrollIDStatus: %+v", session.ScrollIDStatus)
}
