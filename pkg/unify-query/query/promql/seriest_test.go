// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestSeriesSetBaseUsage
func TestSeriesSetBaseUsage(t *testing.T) {
	log.InitTestLogger()

	testData := []struct {
		tables *influxdb.Tables
		rounds int
	}{
		// 正常使用
		{
			tables: &influxdb.Tables{
				Tables: make([]*influxdb.Table, 3),
			},
			rounds: 3,
		},
		// 为空的情况
		{
			tables: &influxdb.Tables{
				Tables: nil,
			},
			rounds: 0,
		},
		// 只有一个结果表的特殊情况
		{
			tables: &influxdb.Tables{
				Tables: make([]*influxdb.Table, 1),
			},
			rounds: 1,
		},
	}

	for _, data := range testData {
		seriesSet := NewInfluxdbSeriesSet(data.tables)
		for range make([]bool, data.rounds) {
			assert.True(t, seriesSet.Next(), "failed with rounds->[%d]", data.rounds)
		}
		assert.False(t, seriesSet.Next())
	}
}

// TestSeriesAnalysis
func TestSeriesAnalysis(t *testing.T) {
	log.InitTestLogger()

	testData := []struct {
		table   *influxdb.Table
		isError bool
		labels  map[string]string
	}{
		// 正常使用情况
		{
			table: &influxdb.Table{
				GroupKeys:   []string{"bk_biz_id", "bk_target_ip", "bk_target_cloud_id"},
				GroupValues: []string{"2", "127.0.0.1", "0"},
			},
			isError: false,
			labels: map[string]string{
				"bk_biz_id":          "2",
				"bk_target_ip":       "127.0.0.1",
				"bk_target_cloud_id": "0",
			},
		},
		// 特殊的GroupKey和GroupValue不对齐的情况
		{
			table: &influxdb.Table{
				GroupValues: []string{"value1", "value2"},
				GroupKeys:   []string{"key1"},
			},
			isError: true,
		},
	}

	for round, data := range testData {
		result := NewInfluxdbSeries(data.table)
		if data.isError {
			assert.Nil(t, result, "round->[%d] result table is bad nothing will result", round)
			continue
		}

		labelResult := result.Labels()
		assert.Equal(t, len(data.labels), len(labelResult), "round->[%d] labels length match", round)

		labelMap := make(map[string]bool)

		for _, label := range labelResult {
			// 确保label没有重复的
			_, ok := labelMap[label.Name]
			assert.Falsef(t, ok, "round->[%d] check for label replicate", round)
			// 所有的label解析符合预期
			assert.Equalf(t, data.labels[label.Name], label.Value, "rounds->[%d] label match failed", round)
		}
	}
}
