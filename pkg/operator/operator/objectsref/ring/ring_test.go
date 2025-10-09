// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ring

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRing(t *testing.T) {
	t.Run("ring 1", func(t *testing.T) {
		q := New(5)
		rv := q.Put(strconv.Itoa(0))

		assert.Equal(t, ResourceVersion(1), rv)
		assert.Equal(t, ResourceVersion(1), q.MinResourceVersion())
		assert.Equal(t, ResourceVersion(1), q.MaxResourceVersion())

		objs := []any{"0"}
		assert.Equal(t, objs, q.ReadGt(0))
	})

	t.Run("ring 2", func(t *testing.T) {
		q := New(5)
		var rv ResourceVersion
		for i := 0; i < 5; i++ {
			rv = q.Put(strconv.Itoa(i))
		}

		assert.Equal(t, ResourceVersion(5), rv)
		assert.Equal(t, ResourceVersion(1), q.MinResourceVersion())
		assert.Equal(t, ResourceVersion(5), q.MaxResourceVersion())

		objs := []any{"0", "1", "2", "3", "4"}
		assert.Equal(t, objs, q.ReadGt(0))
	})

	t.Run("ring with start index", func(t *testing.T) {
		q := New(5)
		for i := 0; i < 5; i++ {
			q.Put(strconv.Itoa(i))
		}
		objs := []any{"3", "4"}
		assert.Equal(t, objs, q.ReadGt(3))
	})

	t.Run("ring oversize", func(t *testing.T) {
		q := New(5)
		for i := 0; i < 7; i++ {
			q.Put(strconv.Itoa(i))
		}

		assert.Equal(t, ResourceVersion(3), q.MinResourceVersion())
		assert.Equal(t, ResourceVersion(7), q.MaxResourceVersion())

		objs := []any{"2", "3", "4", "5", "6"}
		assert.Equal(t, objs, q.ReadGt(0))
	})

	t.Run("ring oversize with start index", func(t *testing.T) {
		q := New(5)
		for i := 0; i < 7; i++ {
			q.Put(strconv.Itoa(i))
		}
		objs := []any{"5", "6"}
		assert.Equal(t, objs, q.ReadGt(5))
	})
}
