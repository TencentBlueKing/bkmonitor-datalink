// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIntMathCeil(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
		want int64
	}{
		{
			name: "normal division",
			a:    10,
			b:    3,
			want: 4, // ceil(10/3) = ceil(3.33) = 4
		},
		{
			name: "exact division",
			a:    10,
			b:    2,
			want: 5, // ceil(10/2) = ceil(5) = 5
		},
		{
			name: "a less than b",
			a:    3,
			b:    10,
			want: 1, // ceil(3/10) = ceil(0.3) = 1
		},
		{
			name: "a equals b",
			a:    5,
			b:    5,
			want: 1, // ceil(5/5) = ceil(1) = 1
		},
		{
			name: "large numbers",
			a:    1000,
			b:    333,
			want: 4, // ceil(1000/333) = ceil(3.003) = 4
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := intMathCeil(tt.a, tt.b)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestIntMathFloor(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
		want int64
	}{
		{
			name: "normal division",
			a:    10,
			b:    3,
			want: 3, // floor(10/3) = floor(3.33) = 3
		},
		{
			name: "exact division",
			a:    10,
			b:    2,
			want: 5, // floor(10/2) = floor(5) = 5
		},
		{
			name: "a less than b",
			a:    3,
			b:    10,
			want: 0, // floor(3/10) = floor(0.3) = 0
		},
		{
			name: "a equals b",
			a:    5,
			b:    5,
			want: 1, // floor(5/5) = floor(1) = 1
		},
		{
			name: "divide by zero",
			a:    10,
			b:    0,
			want: 10, // should return a when b is 0
		},
		{
			name: "large numbers",
			a:    1000,
			b:    333,
			want: 3, // floor(1000/333) = floor(3.003) = 3
		},
		{
			name: "negative result",
			a:    -10,
			b:    3,
			want: -4, // floor(-10/3) = floor(-3.33) = -4
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := intMathFloor(tt.a, tt.b)
			assert.Equal(t, tt.want, result)
		})
	}
}
