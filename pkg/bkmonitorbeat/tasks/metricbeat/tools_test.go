// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricbeat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAlignTs(t *testing.T) {
	type Case struct {
		period  int
		nowSecs int
		wait    int
	}

	cases := []Case{
		{period: 10, nowSecs: 10, wait: 0},
		{period: 10, nowSecs: 13, wait: 7},
		{period: 15, nowSecs: 13, wait: 2},
		{period: 30, nowSecs: 13, wait: 17},
		{period: 60, nowSecs: 13, wait: 47},
		{period: 120, nowSecs: 13, wait: 0},
		{period: 15, nowSecs: 14, wait: 1},
	}

	for _, tt := range cases {
		wait := alignTs(tt.period, tt.nowSecs)
		assert.Equal(t, tt.wait, wait)
	}
}
