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
	"strings"
	"sync"

	"github.com/cespare/xxhash/v2"
	"golang.org/x/exp/slices"
)

var seps = []byte{'\xff'}

type Label struct {
	Name  string
	Value string
}

type Labels []Label

func (ls Labels) Len() int { return len(ls) }

func (ls Labels) Swap(i, j int) { ls[i], ls[j] = ls[j], ls[i] }

func (ls Labels) Less(i, j int) bool { return ls[i].Name < ls[j].Name }

var bytesPool = sync.Pool{
	New: func() any {
		return make([]byte, 0, 1024)
	},
}

// Hash 计算 Labels hash 不做排序保证 需要在调用前自行排序
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

var labelsPool = sync.Pool{
	New: func() any {
		return make(Labels, 0)
	},
}

// HashFromMap 返回 m hash 值
func HashFromMap(m map[string]string) uint64 {
	lbs := labelsPool.Get().(Labels)
	lbs = lbs[:0]
	for k, v := range m {
		lbs = append(lbs, Label{Name: k, Value: v})
	}

	// slices.SortFunc 经测试要快于 sort.Sort
	slices.SortFunc(lbs, func(a, b Label) int {
		return strings.Compare(a.Name, b.Name)
	})
	h := lbs.Hash()
	lbs = lbs[:0]
	labelsPool.Put(lbs) // nolint:staticcheck
	return h
}
