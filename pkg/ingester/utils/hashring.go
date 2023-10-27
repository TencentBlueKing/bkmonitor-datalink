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
	"fmt"
	"sort"
)

// IDer
type IDer interface {
	ID() int
}

type balanceElements []IDer

// Len
func (b balanceElements) Len() int {
	return len(b)
}

// Swap
func (b balanceElements) Swap(i, j int) {
	tmp := b[i]
	b[i] = b[j]
	b[j] = tmp
}

// Less
func (b balanceElements) Less(i, j int) bool {
	return b[i].ID() < b[j].ID()
}

// Balance : balance items into nodes
func Balance(items []IDer, nodes []IDer) map[IDer][]IDer {
	sort.Sort(balanceElements(items))
	sort.Sort(balanceElements(nodes))

	nodeLength := len(nodes)
	if nodeLength <= 0 {
		return nil
	}
	balanceLength := len(items) / nodeLength
	mapping := make(map[int]*struct {
		Node  IDer
		Items []IDer
	}, len(nodes))
	for index, node := range nodes {
		mapping[index] = &struct {
			Node  IDer
			Items []IDer
		}{
			Node:  node,
			Items: make([]IDer, 0, balanceLength),
		}
	}

	for _, item := range items {
		id := item.ID()
		index := id % nodeLength
		pair := mapping[index]
		pair.Items = append(pair.Items, item)
	}

	result := make(map[IDer][]IDer, nodeLength)
	for _, pair := range mapping {
		result[pair.Node] = pair.Items
	}

	return result
}

// BalanceElement
type BalanceElement struct {
	id int
}

// ID
func (e *BalanceElement) ID() int {
	return e.id
}

// NewIDBalanceElement
func NewIDBalanceElement(id int) *BalanceElement {
	return &BalanceElement{
		id: id,
	}
}

// NewIDBalanceElements
func NewIDBalanceElements(base int, repeat int) []*BalanceElement {
	els := make([]*BalanceElement, 0, repeat)
	for i := 0; i < repeat; i++ {
		els = append(els, NewIDBalanceElement(base+i))
	}

	return els
}

// DetailsBalanceElement
type DetailsBalanceElement struct {
	*BalanceElement
	Details interface{}
}

// String
func (e *DetailsBalanceElement) String() string {
	return fmt.Sprintf("%v:%d", e.Details, e.id)
}

// NewDetailsBalanceElement
func NewDetailsBalanceElement(details interface{}, id int) *DetailsBalanceElement {
	return &DetailsBalanceElement{
		BalanceElement: NewIDBalanceElement(id),
		Details:        details,
	}
}

// NewDetailsBalanceElements
func NewDetailsBalanceElements(details interface{}, count int) []IDer {
	return NewDetailsBalanceElementsWithID(HashItInt(details), details, count)
}

// NewDetailsBalanceElementsWithID
func NewDetailsBalanceElementsWithID(id int, details interface{}, count int) []IDer {
	els := make([]IDer, count)
	bases := NewIDBalanceElements(id, count)

	for i := range els {
		els[i] = &DetailsBalanceElement{
			BalanceElement: bases[i],
			Details:        details,
		}
	}

	return els
}
