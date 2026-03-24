// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function

import (
	"testing"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
)

func TestMergeSamples(t *testing.T) {
	t1 := []prompb.Sample{
		{
			Timestamp: 1734462839000,
			Value:     5,
		},
		{
			Timestamp: 1734462719000,
			Value:     0.1,
		},
		{
			Timestamp: 1734462719000,
			Value:     0.2,
		},
	}

	t2 := []prompb.Sample{
		{
			Timestamp: 1734462779000,
			Value:     2,
		},
		{
			Timestamp: 1734462719000,
			Value:     3,
		},
	}

	t3 := MergeSamplesWithFuncAndSort(Sum)(t1, t2)
	assert.Equal(t, []prompb.Sample{
		{
			Timestamp: 1734462719000,
			Value:     3.3,
		},
		{
			Timestamp: 1734462779000,
			Value:     2,
		},
		{
			Timestamp: 1734462839000,
			Value:     5,
		},
	}, t3)

	t4 := MergeSamplesWithFuncAndSort(Min)(t1, t2)
	assert.Equal(t, []prompb.Sample{
		{
			Timestamp: 1734462719000,
			Value:     0.1,
		},
		{
			Timestamp: 1734462779000,
			Value:     2,
		},
		{
			Timestamp: 1734462839000,
			Value:     5,
		},
	}, t4)

	t5 := MergeSamplesWithFuncAndSort(Max)(t1, t2)
	assert.Equal(t, []prompb.Sample{
		{
			Timestamp: 1734462719000,
			Value:     3,
		},
		{
			Timestamp: 1734462779000,
			Value:     2,
		},
		{
			Timestamp: 1734462839000,
			Value:     5,
		},
	}, t5)

	t6 := MergeSamplesWithFuncAndSort(Avg)(t1, t2)
	assert.Equal(t, []prompb.Sample{
		{
			Timestamp: 1734462719000,
			Value:     1.0999999999999999,
		},
		{
			Timestamp: 1734462779000,
			Value:     2,
		},
		{
			Timestamp: 1734462839000,
			Value:     5,
		},
	}, t6)

	t8 := MergeSamplesWithUnionAndSort(t1, t2)
	assert.Equal(t, []prompb.Sample{
		{
			Timestamp: 1734462719000,
			Value:     0.1,
		},
		{
			Timestamp: 1734462719000,
			Value:     0.2,
		},
		{
			Timestamp: 1734462719000,
			Value:     3,
		},
		{
			Timestamp: 1734462779000,
			Value:     2,
		},
		{
			Timestamp: 1734462839000,
			Value:     5,
		},
	}, t8)
}
