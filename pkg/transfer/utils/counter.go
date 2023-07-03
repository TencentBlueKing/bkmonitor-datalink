// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"github.com/emirpasic/gods/maps/treemap"
	"github.com/emirpasic/gods/utils"
)

// CounterComparator
type CounterComparator = utils.Comparator

// StringCounterComparator
var StringCounterComparator = utils.StringComparator

// Counter
type Counter struct {
	*treemap.Map
}

// Incr
func (d *Counter) Incr(item interface{}) {
	count := 1

	value, ok := d.Map.Get(item)
	if ok {
		count = 1 + value.(int)
	}

	d.Map.Put(item, count)
}

// Desc
func (d *Counter) Desc(item interface{}) {
	count := -1

	value, ok := d.Map.Get(item)
	if ok {
		count = value.(int) - 1
	}

	d.Map.Put(item, count)
}

// Visit
func (d *Counter) Visit(fn func(item interface{}, count int)) {
	d.Each(func(item interface{}, value interface{}) {
		fn(item, value.(int))
	})
}

// Sum
func (d *Counter) Sum() int {
	sum := 0
	d.Visit(func(item interface{}, count int) {
		sum += count
	})
	return sum
}

// NewCounter
func NewCounter(comparator CounterComparator) *Counter {
	return &Counter{
		Map: treemap.NewWith(comparator),
	}
}
