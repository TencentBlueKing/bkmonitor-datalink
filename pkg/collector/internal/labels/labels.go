// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package labels

import (
	"sort"
	"sync"

	"github.com/cespare/xxhash/v2"
)

var seps = []byte{'\xff'}

// Label is a key/value pairs of strings.
//
//go:generate msgp
type Label struct {
	Name  string `msg:"n"`
	Value string `msg:"v"`
}

// Labels is a sorted set of labels. Order has to be guaranteed upon
// instantiation.
type Labels []Label

func (ls Labels) Len() int           { return len(ls) }
func (ls Labels) Swap(i, j int)      { ls[i], ls[j] = ls[j], ls[i] }
func (ls Labels) Less(i, j int) bool { return ls[i].Name < ls[j].Name }

var bytesPool = sync.Pool{
	New: func() any {
		return make([]byte, 0, 1024)
	},
}

// Hash returns a hash value for the label set.
func (ls Labels) Hash() uint64 {
	b := bytesPool.Get().([]byte)
	b = b[:0]
	for _, v := range ls {
		b = append(b, v.Name...)
		b = append(b, seps[0])
		b = append(b, v.Value...)
		b = append(b, seps[0])
	}
	h := xxhash.Sum64(b)
	b = b[:0]
	bytesPool.Put(b) // nolint:staticcheck
	return h
}

// Map returns a string map of the labels.
func (ls Labels) Map() map[string]string {
	m := make(map[string]string, len(ls))
	for _, l := range ls {
		m[l.Name] = l.Value
	}
	return m
}

// FromMap returns new sorted Labels from the given map.
func FromMap(m map[string]string) Labels {
	lbs := make(Labels, 0, len(m))
	for k, v := range m {
		lbs = append(lbs, Label{Name: k, Value: v})
	}
	sort.Sort(lbs)
	return lbs
}

var labelsPool = sync.Pool{
	New: func() any {
		return make(Labels, 0)
	},
}

// HashFromMap returns has id for the given dimensions
func HashFromMap(m map[string]string) uint64 {
	lbs := labelsPool.Get().(Labels)
	lbs = lbs[:0]
	for k, v := range m {
		lbs = append(lbs, Label{Name: k, Value: v})
	}
	sort.Sort(lbs)
	h := lbs.Hash()
	lbs = lbs[:0]
	labelsPool.Put(lbs) // nolint:staticcheck
	return h
}
