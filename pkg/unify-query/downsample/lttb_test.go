// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package downsample

import (
	"testing"

	"github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/assert"
)

func TestLttbFunc(t *testing.T) {
	data := []promql.Point{
		{T: 0, V: 0}, // sentinel value
		{T: 1299456, V: 116.3707},
		{T: 1300320, V: 116.3752}, // a
		{T: 1301184, V: 116.3648}, // b --> Should be selected even when triangle area is zero.
		{T: 1302048, V: 116.3544}, // c
		{T: 1302912, V: 116.3328},
		{T: 1306368, V: 116.3277},
		{T: 1307232, V: 116.2676},
	}

	want := map[int][]promql.Point{
		7: {
			{T: 0, V: 0},
			{T: 1299456, V: 116.3707},
			{T: 1300320, V: 116.3752},
			{T: 1301184, V: 116.3648},
			{T: 1302048, V: 116.3544},
			{T: 1306368, V: 116.3277},
			{T: 1307232, V: 116.2676},
		},
		3: {
			{T: 0, V: 0},
			{T: 1299456, V: 116.3707},
			{T: 1307232, V: 116.2676},
		},
	}

	for _, threshold := range []int{3, 7} {
		have := lttbFunc(data, threshold)
		for k, v := range have {
			assert.Equal(t, want[threshold][k], v)
		}
	}
}
