// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

type LabelsCache[T any] struct {
	Map     map[int64]map[LabelsHash]*LabelsCacheEntry[T]
	Factory func() *T
}

func NewLabelsCache[T any](factory func() *T) LabelsCache[T] {
	return LabelsCache[T]{
		Map:     make(map[int64]map[LabelsHash]*LabelsCacheEntry[T]),
		Factory: factory,
	}
}

type LabelsCacheEntry[T any] struct {
	Labels Labels
	Value  *T
}

func (c *LabelsCache[T]) NewCacheEntry(l Labels) *LabelsCacheEntry[T] {
	return &LabelsCacheEntry[T]{
		Labels: CopyLabels(l),
		Value:  c.Factory(),
	}
}

func (c *LabelsCache[T]) GetOrCreateTree(sampleType int64, l Labels) *LabelsCacheEntry[T] {
	h := l.Hash()

	p, ok := c.Map[sampleType]
	if !ok {
		e := c.NewCacheEntry(l)
		c.Map[sampleType] = map[LabelsHash]*LabelsCacheEntry[T]{h: e}
		return e
	}
	e, found := p[h]
	if !found {
		e = c.NewCacheEntry(l)
		p[h] = e
	}
	return e
}

func (c *LabelsCache[T]) GetOrCreate(sampleType int64, l Labels) *LabelsCacheEntry[T] {
	h := l.Hash()

	p, ok := c.Map[sampleType]
	if !ok {
		e := c.NewCacheEntry(l)
		c.Map[sampleType] = map[LabelsHash]*LabelsCacheEntry[T]{h: e}
		return e
	}
	e, found := p[h]
	if !found {
		e = c.NewCacheEntry(l)
		p[h] = e
	}
	return e
}

func (c *LabelsCache[T]) Get(sampleType int64, h LabelsHash) (*LabelsCacheEntry[T], bool) {
	p, ok := c.Map[sampleType]
	if !ok {
		return nil, false
	}
	x, ok := p[h]
	return x, ok
}

func (c *LabelsCache[T]) Put(sampleType int64, e *LabelsCacheEntry[T]) {
	p, ok := c.Map[sampleType]
	if !ok {
		p = make(map[LabelsHash]*LabelsCacheEntry[T])
		c.Map[sampleType] = p
	}
	p[e.Labels.Hash()] = e
}

func (c *LabelsCache[T]) Remove(sampleType int64, h LabelsHash) {
	p, ok := c.Map[sampleType]
	if !ok {
		return
	}
	delete(p, h)
	if len(p) == 0 {
		delete(c.Map, sampleType)
	}
}

func CopyLabels(labels Labels) Labels {
	var ls []*Label
	for _, v := range labels.Items {
		ls = append(ls, CopyLabel(v))
	}

	return NewLabels(ls)
}

// CutLabel creates a copy of labels without label i.
func CutLabel(labels Labels, i int) Labels {
	c := make([]*Label, 0, len(labels.Items)-1)
	for j, label := range labels.Items {
		if i != j {
			c = append(c, CopyLabel(label))
		}
	}

	return NewLabels(c)
}

func CopyLabel(label *Label) *Label {
	return &Label{
		Key:   label.Key,
		Value: label.Value,
	}
}
