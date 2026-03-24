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
	"sort"
)

type Order uint8

const (
	OrderDesc Order = iota // 降序
	OrderAsce              // 升序
	OrderNone              // 不排序
)

type uniqueStrings struct {
	order  Order
	marker map[string]bool
	store  []string
}

func newUniqueStrings(order Order) *uniqueStrings {
	return &uniqueStrings{
		order:  order,
		marker: make(map[string]bool),
		store:  make([]string, 0),
	}
}

func (us *uniqueStrings) Set(s string) {
	if us.marker[s] {
		return
	}

	us.marker[s] = true
	us.store = append(us.store, s)

	switch us.order {
	case OrderAsce:
		sort.Slice(us.store, func(i, j int) bool {
			return us.store[i] < us.store[j]
		})
	case OrderDesc:
		sort.Slice(us.store, func(i, j int) bool {
			return us.store[i] > us.store[j]
		})
	}
}

func (us *uniqueStrings) Get() []string {
	return us.store
}

// MapStrings 不保证线程安全 调用方需自行保证
type MapStrings struct {
	order Order
	store map[string]*uniqueStrings
}

func New(order Order) *MapStrings {
	return &MapStrings{
		order: order,
		store: make(map[string]*uniqueStrings),
	}
}

func (ms *MapStrings) Set(key, val string) {
	_, ok := ms.store[key]
	if !ok {
		ms.store[key] = newUniqueStrings(ms.order)
	}
	ms.store[key].Set(val)
}

func (ms *MapStrings) Get(key string) []string {
	v, ok := ms.store[key]
	if !ok {
		return nil
	}
	return v.Get()
}

func (ms *MapStrings) Len() int {
	return len(ms.store)
}
