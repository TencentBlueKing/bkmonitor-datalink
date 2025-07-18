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
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

func TestScrollSession_ElasticsearchFlow(t *testing.T) {
	ctx := context.Background()

	s := &ScrollSession{
		Key:           "test_es_session",
		CreateAt:      time.Now(),
		LastAccessAt:  time.Now(),
		ScrollTimeout: 30 * time.Second,
		MaxSlice:      3,
		Limit:         10,
		Index:         0,
		ScrollIDs:     make(map[string]string),
		SliceStatus:   make(map[string]bool),
		Status:        SessionStatusRunning,
	}

	conn := "es-cluster-1"
	tID := "metrics_table"
	st := consul.ElasticsearchStorageType

	ss, err := s.MakeSlices(ctx, st, conn, tID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(ss), "应该生成3个分片")

	for i, slice := range ss {
		assert.Equal(t, i, slice.SliceIndex, "分片索引应该正确")
		assert.Equal(t, "", slice.ScrollID, "初始时 ScrollID 应该为空")
		assert.Equal(t, 0, slice.Index, "ES 不使用 Index 字段")
	}

	for i, slice := range ss {
		scrollID := fmt.Sprintf("DXF1ZXJ5QW5kRmV0Y2g%d", i)
		s.SetScrollID(conn, tID, scrollID, slice.SliceIndex)

		rID, index, err := s.CurrentScrollID(
			ctx,
			st,
			conn,
			tID,
			slice.SliceIndex,
		)
		assert.NoError(t, err)
		assert.Equal(t, scrollID, rID)
		assert.Equal(t, 0, index, "ES 类型 index 应该为 0")
	}

	assert.True(t, s.HasMoreData(st), "设置了 ScrollID 后应该还有更多数据")

	s.MarkSliceDone(conn, tID, 0)
	s.RemoveScrollID(conn, tID, 0)

	assert.True(t, s.HasMoreData(st), "部分分片完成后仍应该有更多数据")

	for i := 1; i < 3; i++ {
		s.MarkSliceDone(conn, tID, i)
		s.RemoveScrollID(conn, tID, i)
	}

	assert.False(t, s.HasMoreData(st), "所有分片完成后应该没有更多数据")
}

func TestScrollSession_DorisFlow(t *testing.T) {
	ctx := context.Background()

	session := &ScrollSession{
		Key:           "test_doris_session",
		CreateAt:      time.Now(),
		LastAccessAt:  time.Now(),
		ScrollTimeout: 30 * time.Second,
		MaxSlice:      2,
		Limit:         20,
		Index:         0,
		ScrollIDs:     make(map[string]string),
		SliceStatus:   make(map[string]bool),
		Status:        SessionStatusRunning,
	}

	storageType := consul.BkSqlStorageType

	slices, err := session.MakeSlices(ctx, storageType, "", "")
	require.NoError(t, err)
	assert.Equal(t, 2, len(slices), "应该生成2个分片")

	es := []int{0, 1}
	for i, slice := range slices {
		assert.Equal(t, i, slice.SliceIndex, "分片索引应该正确")
		assert.Equal(t, "", slice.ScrollID, "Doris 不使用 ScrollID")
		assert.Equal(t, es[i], slice.Index, "Index 应该递增")
	}

	assert.Equal(t, 2, session.Index, "session Index 应该已经递增到2")

	assert.True(t, session.HasMoreData(storageType), "Running 状态应该有更多数据")

	session.Status = SessionStatusDone
	assert.False(t, session.HasMoreData(storageType), "Done 状态应该没有更多数据")

	session.Status = SessionStatusRunning
	_, nIdx, err := session.getNextDorisIndex(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, nIdx, "下一个索引应该是2")
	assert.Equal(t, 3, session.Index, "session Index 应该继续递增")
}

func TestScrollSession_MultipleConnectAndTable(t *testing.T) {
	ctx := context.Background()

	s := &ScrollSession{
		Key:           "test_multi_session",
		CreateAt:      time.Now(),
		LastAccessAt:  time.Now(),
		ScrollTimeout: 30 * time.Second,
		MaxSlice:      2,
		Limit:         10,
		Index:         0,
		ScrollIDs:     make(map[string]string),
		SliceStatus:   make(map[string]bool),
		Status:        SessionStatusRunning,
	}

	st := consul.ElasticsearchStorageType

	conns := []string{"es-cluster-1", "es-cluster-2"}
	ts := []string{"table1", "table2"}

	sc := 0
	for _, c := range conns {
		for _, table := range ts {
			for sIdx := 0; sIdx < s.MaxSlice; sIdx++ {
				sID := fmt.Sprintf("scroll_%d", sc)
				s.SetScrollID(c, table, sID, sIdx)
				sc++
			}
		}
	}

	sc = 0
	for _, c := range conns {
		for _, table := range ts {
			for sliceIdx := 0; sliceIdx < s.MaxSlice; sliceIdx++ {
				eSID := fmt.Sprintf("scroll_%d", sc)
				aSID, _, err := s.CurrentScrollID(
					ctx,
					st,
					c,
					table,
					sliceIdx,
				)
				assert.NoError(t, err)
				assert.Equal(t, eSID, aSID)
				sc++
			}
		}
	}

	assert.True(t, s.HasMoreData(st))

	fConn := conns[0]
	fTable := ts[0]
	for sIdx := 0; sIdx < s.MaxSlice; sIdx++ {
		s.MarkSliceDone(fConn, fTable, sIdx)
		s.RemoveScrollID(fConn, fTable, sIdx)
	}

	assert.True(t, s.HasMoreData(st))

	for _, c := range conns {
		for _, t := range ts {
			if c == fConn && t == fTable {
				continue
			}
			for sIdx := 0; sIdx < s.MaxSlice; sIdx++ {
				s.MarkSliceDone(c, t, sIdx)
				s.RemoveScrollID(c, t, sIdx)
			}
		}
	}

	assert.False(t, s.HasMoreData(st))
}
