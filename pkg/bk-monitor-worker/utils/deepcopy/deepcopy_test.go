// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package deepcopy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeepCopy(t *testing.T) {
	t.Parallel()

	type s struct {
		A float64
		B int
		C []int
		D *int
		E map[string]int
	}
	d := 3
	dst := new(s)
	src := s{1.0, 1, []int{1, 2, 3}, &d, map[string]int{"a": 1}}

	err := DeepCopy(dst, &src)
	src.A = 2

	assert.NoError(t, err)
	assert.Equal(t, 1.0, dst.A)
	assert.Equal(t, 1, dst.B)
	assert.Equal(t, []int{1, 2, 3}, dst.C)
	assert.Equal(t, &d, dst.D)
	assert.Equal(t, map[string]int{"a": 1}, dst.E)
}
