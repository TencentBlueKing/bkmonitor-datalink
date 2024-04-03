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

func TestLabelsSwap(t *testing.T) {
	tests := []struct {
		name  string
		items []*Label
		i     int
		j     int
		want  []*Label
	}{
		{
			name: "swap two elements",
			items: []*Label{
				{Key: 1, Value: 10},
				{Key: 2, Value: 20},
			},
			i: 0,
			j: 1,
			want: []*Label{
				{Key: 2, Value: 20},
				{Key: 1, Value: 10},
			},
		},
		{
			name: "swap same elements",
			items: []*Label{
				{Key: 1, Value: 10},
				{Key: 2, Value: 20},
			},
			i: 0,
			j: 0,
			want: []*Label{
				{Key: 1, Value: 10},
				{Key: 2, Value: 20},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := Labels{Items: tt.items}
			l.Swap(tt.i, tt.j)
			for i, item := range l.Items {
				if item.Key != tt.want[i].Key || item.Value != tt.want[i].Value {
					t.Errorf("Swap() left = %v, right = %v", l.Items, tt.want)
				}
			}
		})
	}
}

func TestLabelCacheGetOrCreateTree(t *testing.T) {
	cache := NewLabelsCache(func() *int {
		value := 1
		return &value
	})

	sampleType := int64(1)
	labels := NewLabels([]*Label{{Key: 1, Value: 2}})

	cacheEntry := cache.GetOrCreateTree(sampleType, labels)
	assert.NotNil(t, cacheEntry)
	assert.Equal(t, len(cacheEntry.Labels.Items), len(labels.Items))
	assert.Equal(t, *cacheEntry.Value, 1)
	for i, label := range cacheEntry.Labels.Items {
		assert.Equal(t, label.Key, labels.Items[i].Key)
		assert.Equal(t, label.Value, labels.Items[i].Value)
	}

	cacheEntry2 := cache.GetOrCreateTree(sampleType, labels)
	assert.Equal(t, cacheEntry, cacheEntry2)
}

func TestLabelCacheGetOrCreate(t *testing.T) {
	cache := NewLabelsCache(func() *int {
		value := 1
		return &value
	})

	sampleType := int64(1)
	labels := NewLabels([]*Label{{Key: 1, Value: 2}})
	cacheEntry := cache.GetOrCreate(sampleType, labels)
	assert.NotNil(t, cacheEntry)

	assert.Equal(t, len(cacheEntry.Labels.Items), len(labels.Items))
	for i, label := range cacheEntry.Labels.Items {
		assert.Equal(t, label.Key, labels.Items[i].Key)
		assert.Equal(t, label.Value, labels.Items[i].Value)
	}
	assert.Equal(t, *cacheEntry.Value, 1)

	cacheEntry2 := cache.GetOrCreate(sampleType, labels)
	assert.Equal(t, cacheEntry, cacheEntry2)
}

func TestLabelCacheGetPutRemove(t *testing.T) {
	cache := NewLabelsCache(func() *int {
		value := 1
		return &value
	})

	sampleType := int64(1)
	labels := NewLabels([]*Label{{Key: 1, Value: 2}})
	labelsHash := labels.Hash()

	cacheEntry, found := cache.Get(sampleType, labelsHash)
	assert.False(t, found)

	cacheEntry = &LabelsCacheEntry[int]{
		Labels: labels,
		Value:  cache.Factory(),
	}
	cache.Put(sampleType, cacheEntry)

	cacheEntry2, found := cache.Get(sampleType, labelsHash)
	assert.True(t, found)
	assert.Equal(t, cacheEntry, cacheEntry2)

	cache.Remove(sampleType, labelsHash)
	cacheEntry, found = cache.Get(sampleType, labelsHash)
	assert.False(t, found)
}

func TestCopyLabels(t *testing.T) {
	labels := NewLabels([]*Label{{Key: 1, Value: 2}, {Key: 3, Value: 4}})

	c := CopyLabels(labels)
	assert.Equal(t, len(c.Items), len(labels.Items))
	for i, label := range c.Items {
		assert.Equal(t, label.Key, labels.Items[i].Key)
		assert.Equal(t, label.Value, labels.Items[i].Value)
	}

	c.Items[0].Value = 5
	assert.Equal(t, labels.Items[0].Value, int64(2))
}

func TestCutLabel(t *testing.T) {
	labels := NewLabels([]*Label{{Key: 1, Value: 2}, {Key: 3, Value: 4}})

	cut := CutLabel(labels, 0)
	assert.Equal(t, len(cut.Items), len(labels.Items)-1)

	assert.Equal(t, cut.Items[0].Key, labels.Items[1].Key)
	assert.Equal(t, cut.Items[0].Value, labels.Items[1].Value)
}
