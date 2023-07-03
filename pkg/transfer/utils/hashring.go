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
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

func NewHashBalancer() define.Balancer {
	return &hashBalancer{}
}

type hashBalancer struct{}

func (hb *hashBalancer) Balance(_ define.PlanWithFlows, items []define.IDer, nodes []define.IDer) (define.IDerMapDetailed, define.FlowItems, define.AutoError) {
	var flows define.FlowItems
	idermap := define.NewIDerMapDetailed()

	sort.Slice(items, func(i, j int) bool { return items[i].ID() < items[j].ID() })
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() < nodes[j].ID() })

	nodeLength := len(nodes)
	if nodeLength <= 0 {
		return idermap, flows, define.AutoErrorNoNodes
	}

	balanceLength := len(items) / nodeLength
	mapping := make(map[int]*struct {
		Node  define.IDer
		Items []define.IDer
	}, len(nodes))

	for index, node := range nodes {
		mapping[index] = &struct {
			Node  define.IDer
			Items []define.IDer
		}{
			Node:  node,
			Items: make([]define.IDer, 0, balanceLength),
		}
	}

	for _, item := range items {
		id := item.ID()
		index := id % nodeLength
		pair := mapping[index]
		pair.Items = append(pair.Items, item)
	}

	result := make(map[define.IDer][]define.IDer)
	for _, pair := range mapping {
		result[pair.Node] = pair.Items
	}
	idermap.All = result

	return idermap, flows, define.AutoErrorNil
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
func NewDetailsBalanceElements(details interface{}, count int) []define.IDer {
	return NewDetailsBalanceElementsWithID(HashItInt(details), details, count)
}

// NewDetailsBalanceElementsWithID
func NewDetailsBalanceElementsWithID(id int, details interface{}, count int) []define.IDer {
	els := make([]define.IDer, count)
	bases := NewIDBalanceElements(id, count)

	for i := range els {
		els[i] = &DetailsBalanceElement{
			BalanceElement: bases[i],
			Details:        details,
		}
	}

	return els
}

func NewNodeWithID(id string, details interface{}) define.IDer {
	ret := &DetailsBalanceElement{
		BalanceElement: NewIDBalanceElement(HashItInt(id)),
		Details:        details,
	}

	// bkmonitorv3-2604497288
	sp := strings.Split(id, "-")
	if len(sp) != 2 {
		return ret
	}

	i, err := strconv.Atoi(sp[1])
	if err != nil {
		return ret
	}

	return &DetailsBalanceElement{
		BalanceElement: NewIDBalanceElement(i),
		Details:        details,
	}
}
