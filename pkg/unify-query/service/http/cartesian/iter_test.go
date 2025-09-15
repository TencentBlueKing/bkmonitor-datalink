// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cartesian

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIter
func TestIter(t *testing.T) {
	c := Iter([]any{"a", "b"}, []any{"1", "2", "3"}, []any{"&"})
	result := make([]any, 0)

	for r := range c {
		result = append(result, r)
	}

	assert.Equal(t, 6, len(result))

	for _, r := range result {
		assert.Equal(t, 3, len(r.([]any)))
	}
}
