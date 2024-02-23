// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLabels(t *testing.T) {
	t.Run("sort labels", func(t *testing.T) {
		labels := NewLabels([]*Label{
			{Key: 3, Value: 1},
			{Key: 1, Value: 2},
			{Key: 2, Value: 3},
		})

		assert.Equal(t, 3, labels.Len())
		assert.False(t, labels.Less(0, 1))
		labels.Swap(0, 1)
		assert.True(t, labels.Less(0, 1))
	})

	t.Run("hash labels", func(t *testing.T) {
		labels := NewLabels([]*Label{
			{Key: 1, Value: 1},
			{Key: 2, Value: 2},
			{Key: 3, Value: 0},
		})

		hash := labels.Hash()
		assert.NotEqual(t, LabelsHash(0), hash)
	})

	t.Run("new labels", func(t *testing.T) {
		labels := NewLabels(nil)
		assert.Equal(t, 0, labels.Len())

		labels = NewLabels([]*Label{
			{Key: 1, Value: 1},
		})
		assert.Equal(t, 1, labels.Len())
	})
}

func TestNewLabelsCache(t *testing.T) {
	cache := NewLabelsCache(func() *int {
		i := 0
		return &i
	})

	if cache.Map == nil {
		t.Error("Expected map to be initialized")
	}

	if cache.Factory == nil {
		t.Error("Expected factory to be initialized")
	}
}

func TestNewCacheEntry(t *testing.T) {
	cache := NewLabelsCache(func() *int {
		i := 0
		return &i
	})

	labels := NewLabels([]*Label{{Key: 1, Value: 1}})
	entry := cache.NewCacheEntry(labels)

	if entry.Labels.Len() != 1 {
		t.Error("Expected labels to be copied")
	}

	if *entry.Value != 0 {
		t.Error("Expected value to be initialized by factory")
	}
}
