// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/magiconair/properties/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func jsonEqual(t *testing.T, a, b interface{}) {
	c, _ := json.Marshal(a)
	d, _ := json.Marshal(b)
	assert.Equal(t, string(c), string(d))
}

func TestSegmentedList(t *testing.T) {
	var testCases = []struct {
		name          string
		segmentOption *querySegmentOption
		list          [][2]int64
	}{
		{
			name: "test-1",
			segmentOption: &querySegmentOption{
				start:     0,
				end:       1000,
				interval:  300,
				docCount:  1e5,
				storeSize: MB,
			},
			list: [][2]int64{
				{0, 300},
				{300, 600},
				{600, 1000},
			},
		},
		{
			name: "test-2",
			segmentOption: &querySegmentOption{
				start:     0,
				end:       1000,
				interval:  60,
				docCount:  1e5,
				storeSize: MB,
			},
			list: [][2]int64{
				{0, 120},
				{120, 240},
				{240, 360},
				{360, 480},
				{480, 600},
				{600, 720},
				{720, 840},
				{840, 1000},
			},
		},
	}
	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			qs, err := newRangeSegment(ctx, c.segmentOption)
			assert.Equal(t, err, nil)
			defer qs.close()
			assert.Equal(t, qs.list, c.list)
		})
	}
}
