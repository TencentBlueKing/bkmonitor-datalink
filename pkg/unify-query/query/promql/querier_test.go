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
	"time"

	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/assert"
)

// TestSegmented
func TestSegmented(t *testing.T) {
	for name, res := range map[string]struct {
		queryTimes []QueryTime
		hints      *storage.SelectHints
	}{
		"7d_1m": {
			queryTimes: []QueryTime{
				{Start: 0, End: 121200000},
				{Start: 121200000, End: 242400000},
				{Start: 242400000, End: 363600000},
				{Start: 363600000, End: 484800000},
				{Start: 484800000, End: 604800000},
			},
			hints: &storage.SelectHints{
				Start: 0,
				End:   7 * 24 * time.Hour.Milliseconds(),
				Range: time.Minute.Milliseconds(),
			},
		},
		"10m_1m": {
			queryTimes: []QueryTime{
				{Start: 0, End: 300000},
				{Start: 300000, End: 600000},
			},
			hints: &storage.SelectHints{
				Start: 0,
				End:   10 * time.Minute.Milliseconds(),
				Range: time.Minute.Milliseconds(),
			},
		},
		"1h_20m": {
			queryTimes: []QueryTime{
				{Start: 0, End: 1200000},
				{Start: 1200000, End: 2400000},
				{Start: 2400000, End: 3600000},
			},
			hints: &storage.SelectHints{
				Start: 0,
				End:   time.Hour.Milliseconds(),
				Range: 20 * time.Minute.Milliseconds(),
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			opt := SegmentedOpt{
				Enable:      true,
				MinInterval: "5m",
				MaxRoutines: 5,
				Start:       res.hints.Start,
				End:         res.hints.End,
				Interval:    res.hints.Range,
			}
			queryTime := GetSegmented(opt)
			assert.Equal(t, res.queryTimes, queryTime)
		})
	}
}
