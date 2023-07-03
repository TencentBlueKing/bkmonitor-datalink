// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mapstrings

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestMapStrings(order Order) *MapStrings {
	m := New(order)
	m.Set("k1", "v1")
	m.Set("k1", "v1")
	m.Set("k1", "v4")
	m.Set("k1", "v2")
	m.Set("k1", "")
	m.Set("k1", "v3")
	m.Set("", "v0")
	m.Set("duration/", "")
	return m
}

func TestMapStrings(t *testing.T) {
	t.Run("OrderNone", func(t *testing.T) {
		m := newTestMapStrings(OrderNone)
		assert.Equal(t, m.Get("k1"), []string{"v1", "v4", "v2", "", "v3"})
		assert.Nil(t, m.Get("k2"))
		assert.Equal(t, m.Len(), 3)
		assert.Equal(t, m.Get("duration/"), []string{""})
	})

	t.Run("OrderAsce", func(t *testing.T) {
		m := newTestMapStrings(OrderAsce)
		assert.Equal(t, m.Get("k1"), []string{"", "v1", "v2", "v3", "v4"})
		assert.Nil(t, m.Get("k2"))
		assert.Equal(t, m.Len(), 3)
		assert.Equal(t, m.Get("duration/"), []string{""})
	})

	t.Run("OrderDesc", func(t *testing.T) {
		m := newTestMapStrings(OrderDesc)
		assert.Equal(t, m.Get("k1"), []string{"v4", "v3", "v2", "v1", ""})
		assert.Nil(t, m.Get("k2"))
		assert.Equal(t, m.Len(), 3)
		assert.Equal(t, m.Get("duration/"), []string{""})
	})
}
