// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEffectiveTagValuesLimit(t *testing.T) {
	for name, tc := range map[string]struct {
		requestLimit int
		maxSize      int
		expected     int
	}{
		"request limit": {
			requestLimit: 1,
			maxSize:      10000,
			expected:     1,
		},
		"missing limit": {
			requestLimit: 0,
			maxSize:      10000,
			expected:     10000,
		},
		"negative limit": {
			requestLimit: -1,
			maxSize:      10000,
			expected:     10000,
		},
		"storage maximum": {
			requestLimit: 20000,
			maxSize:      10000,
			expected:     10000,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, effectiveTagValuesLimit(tc.requestLimit, tc.maxSize))
		})
	}
}

func TestSortAndLimitStrings(t *testing.T) {
	values := []string{"c", "a", "b"}
	assert.Equal(t, []string{"a"}, sortAndLimitStrings(values, 1))
	assert.Equal(t, []string{"a", "b", "c"}, sortAndLimitStrings(values, 0))
}
