// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

// BalanceSuite :
type BalanceSuite struct {
	suite.Suite
}

type bel int

// ID :
func (e bel) ID() int {
	return int(e)
}

// TestUsage :
func (s *BalanceSuite) TestUsage() {
	cases := []struct {
		items, nodes []int
		result       map[int][]int
	}{
		{[]int{}, []int{}, map[int][]int{}},
		{[]int{}, []int{1}, map[int][]int{
			1: {},
		}},
		{[]int{}, []int{2, 1}, map[int][]int{
			1: {}, 2: {},
		}},
		{[]int{1}, []int{2, 1}, map[int][]int{
			1: {}, 2: {1},
		}},
		{[]int{1, 2}, []int{2, 1}, map[int][]int{
			1: {2}, 2: {1},
		}},
		{[]int{2, 1}, []int{2, 1}, map[int][]int{
			1: {2}, 2: {1},
		}},
		{[]int{1, 2}, []int{1, 2}, map[int][]int{
			1: {2}, 2: {1},
		}},
		{[]int{2, 1}, []int{1, 2}, map[int][]int{
			1: {2}, 2: {1},
		}},
		{[]int{0, 2}, []int{2, 1}, map[int][]int{
			1: {0, 2}, 2: {},
		}},
		{[]int{0, 1, 2}, []int{2, 1}, map[int][]int{
			1: {0, 2}, 2: {1},
		}},
		{[]int{1, 2}, []int{1}, map[int][]int{
			1: {1, 2},
		}},
	}

	for _, c := range cases {
		items := make([]utils.IDer, 0, len(c.items))
		for _, i := range c.items {
			items = append(items, bel(i))
		}

		nodes := make([]utils.IDer, 0, len(c.nodes))
		for _, i := range c.nodes {
			nodes = append(nodes, bel(i))
		}

		results := utils.Balance(items, nodes)

		for k, values := range c.result {
			mapping := results[bel(k)]
			s.Len(mapping, len(values))
			for i, v := range values {
				s.Equal(bel(v), mapping[i])
			}
		}
	}
}

// TestBalanceSuite :
func TestBalanceSuite(t *testing.T) {
	suite.Run(t, new(BalanceSuite))
}

// DetailsBalanceElementSuite
type DetailsBalanceElementSuite struct {
	suite.Suite
}

// TestHashing
func (s *DetailsBalanceElementSuite) TestHashing() {
	els1 := utils.NewDetailsBalanceElements("test", 1)
	els2 := utils.NewDetailsBalanceElements("hashing", 1)
	s.NotEqual(els1[0].ID(), els2[0].ID())
}

// TestBalance
func (s *DetailsBalanceElementSuite) TestBalance() {
	things := []struct {
		name  string
		count int
	}{
		{"A", 1},
		{"B", 3},
		{"C", 2},
	}

	nodes := []utils.IDer{
		utils.NewIDBalanceElement(1),
		utils.NewIDBalanceElement(2),
	}

	items := make([]utils.IDer, 0)
	for _, thing := range things {
		els := utils.NewDetailsBalanceElements(thing.name, thing.count)
		items = append(items, els...)
	}

	result := utils.Balance(items, nodes)
	s.NotNil(result)
}

// TestDetailsBalanceElementsSuite
func TestDetailsBalanceElementsSuite(t *testing.T) {
	suite.Run(t, new(DetailsBalanceElementSuite))
}
