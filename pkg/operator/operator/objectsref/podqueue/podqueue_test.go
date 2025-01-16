// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podqueue

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPopQueue(t *testing.T) {
	t.Run("ring", func(t *testing.T) {
		q := New(5)
		for i := 0; i < 5; i++ {
			q.Put(Pod{IP: strconv.Itoa(i)})
		}
		pods := []Pod{
			{IP: "0"},
			{IP: "1"},
			{IP: "2"},
			{IP: "3"},
			{IP: "4"},
		}
		assert.Equal(t, pods, q.Pop(0))
	})

	t.Run("ring with start index", func(t *testing.T) {
		q := New(5)
		for i := 0; i < 5; i++ {
			q.Put(Pod{IP: strconv.Itoa(i)})
		}
		pods := []Pod{
			{IP: "3"},
			{IP: "4"},
		}
		assert.Equal(t, pods, q.Pop(3))
	})

	t.Run("overring", func(t *testing.T) {
		q := New(5)
		for i := 0; i < 7; i++ {
			q.Put(Pod{IP: strconv.Itoa(i)})
		}
		pods := []Pod{
			{IP: "2"},
			{IP: "3"},
			{IP: "4"},
			{IP: "5"},
			{IP: "6"},
		}
		assert.Equal(t, pods, q.Pop(0))
	})

	t.Run("overring with start index", func(t *testing.T) {
		q := New(5)
		for i := 0; i < 7; i++ {
			q.Put(Pod{IP: strconv.Itoa(i)})
		}
		pods := []Pod{
			{IP: "5"},
			{IP: "6"},
		}
		assert.Equal(t, pods, q.Pop(5))
	})
}
