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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

type ider int

func (i ider) ID() int { return int(i) }

type flowWithCount struct {
	Count  int
	DataID int
	Flow   int
}

func generate(items []flowWithCount) ([]define.FlowItem, []define.IDer, int) {
	fs := make([]define.FlowItem, 0)
	iders := make([]define.IDer, 0)
	var total int

	for _, item := range items {
		fs = append(fs, define.FlowItem{DataID: item.DataID, Flow: item.Flow})
		for i := 0; i < item.Count; i++ {
			total += item.Flow
			iders = append(iders, ider(item.DataID))
		}
	}

	return fs, iders, total
}

func mockItemIders(items []flowWithCount) []define.IDer {
	iders := make([]define.IDer, 0)
	for _, item := range items {
		for i := 0; i < item.Count; i++ {
			iders = append(iders, ider(item.DataID))
		}
	}
	return iders
}

func mockNodeIders(n int) []define.IDer {
	iders := make([]define.IDer, 0, n)
	for i := 0; i < n; i++ {
		iders = append(iders, ider(i))
	}
	return iders
}

func mockGetFlowsFunc(flows []flowWithCount) func() (define.FlowItems, error) {
	items := make([]define.FlowItem, 0)
	for _, flow := range flows {
		for i := 0; i < flow.Count; i++ {
			items = append(items, define.FlowItem{
				DataID:  flow.DataID,
				Service: fmt.Sprintf("service-%d", i),
				Flow:    flow.Flow,
			})
		}
	}
	return func() (define.FlowItems, error) {
		return items, nil
	}
}

func mockBalanceConfigFunc() define.BalanceConfig {
	return define.DefaultBalanceConfig
}

func TestSameIderList(t *testing.T) {
	balancer := &autoBalancer{}
	cases := []struct {
		Prev []define.IDer
		Next []define.IDer
		Same bool
	}{
		{
			Prev: []define.IDer{ider(1), ider(2), ider(3)},
			Next: []define.IDer{ider(1), ider(2), ider(3)},
			Same: true,
		},
		{
			Prev: []define.IDer{ider(1), ider(2), ider(3), ider(3)},
			Next: []define.IDer{ider(1), ider(2), ider(3)},
			Same: false,
		},
		{
			Prev: []define.IDer{ider(1), ider(2), ider(3)},
			Next: []define.IDer{ider(1), ider(2), ider(4)},
			Same: false,
		},
		{
			Prev: []define.IDer{ider(1), ider(2), ider(3)},
			Next: []define.IDer{ider(1), ider(2)},
			Same: false,
		},
		{
			Prev: []define.IDer{},
			Next: []define.IDer{},
			Same: true,
		},
	}

	for _, c := range cases {
		assert.Equal(t, c.Same, balancer.isSameIderList(c.Prev, c.Next))
	}
}

func TestIsFlowFluctuate(t *testing.T) {
	cases := []struct {
		Fluctuate float64
		Prev      map[string]float64
		Next      map[string]float64
		Ok        bool
	}{
		{
			Fluctuate: 0.15,
			Prev: map[string]float64{
				"a": 0.25, "b": 0.25, "c": 0.25, "d": 0.25,
			},
			Next: map[string]float64{
				"a": 0.24, "b": 0.24, "c": 0.26, "d": 0.26,
			},
			Ok: false,
		},
		{
			Fluctuate: 0.29,
			Prev: map[string]float64{
				"a": 0.25, "b": 0.25, "c": 0.25, "d": 0.25,
			},
			Next: map[string]float64{
				"a": 0.1, "b": 0.2, "c": 0.4, "d": 0.3,
			},
			Ok: true,
		},
	}

	for _, c := range cases {
		balancer := &autoBalancer{fluctuation: c.Fluctuate}
		fluctuated := balancer.isFlowFluctuate(c.Prev, c.Next)
		assert.Equal(t, c.Ok, fluctuated)
	}
}

func TestDraftHugeWeightCalculation(t *testing.T) {
	balancer := &autoBalancer{}

	fs, iders, total := generate([]flowWithCount{
		{Count: 2, DataID: 10, Flow: 110000},
		{Count: 4, DataID: 11, Flow: 100300},
		{Count: 4, DataID: 12, Flow: 102000},
		{Count: 4, DataID: 1002, Flow: 500},
		{Count: 2, DataID: 1003, Flow: 200},
		{Count: 3, DataID: 1004, Flow: 80},
		{Count: 6, DataID: 1005, Flow: 30},
		{Count: 3, DataID: 1006, Flow: 5},
	})

	cases := []struct {
		Num    int
		Groups [][]int
	}{
		{
			Num: 3,
			Groups: [][]int{
				{11, 11, 12, 12, 1002, 1004, 1005, 1006},
				{10, 11, 12, 1002, 1002, 1003, 1004, 1005, 1006},
				{10, 11, 12, 1002, 1003, 1004, 1005, 1005, 1005, 1005, 1006},
			},
		},
		{
			Num: 4,
			Groups: [][]int{
				{11, 12, 1002, 1004, 1005, 1006},
				{11, 12, 1002, 1005, 1005, 1005},
				{10, 11, 12, 1002, 1003, 1004, 1005, 1006},
				{10, 11, 12, 1002, 1003, 1004, 1005, 1006},
			},
		},
	}

	for _, c := range cases {
		draft := balancer.getBestDraft(fs, iders, c.Num)
		assert.Equal(t, c.Num, len(draft.Groups))

		flowsTotal := 0
		for i := 0; i < len(draft.Groups); i++ {
			assert.Equal(t, c.Groups[i], draft.Groups[i].IDs())
			flowsTotal += draft.Groups[i].Flows()
		}

		assert.Equal(t, total, flowsTotal)
	}
}

func TestDraftCalculation(t *testing.T) {
	balancer := &autoBalancer{}

	fs, iders, total := generate([]flowWithCount{
		{Count: 6, DataID: 1001, Flow: 1000},
		{Count: 4, DataID: 1002, Flow: 500},
		{Count: 2, DataID: 1003, Flow: 200},
		{Count: 1, DataID: 1004, Flow: 80},
		{Count: 1, DataID: 1005, Flow: 30},
		{Count: 1, DataID: 1006, Flow: 5},
	})

	cases := []struct {
		Num    int
		Groups [][]int
		Flows  []int
	}{
		{
			Num: 4,
			Groups: [][]int{
				{1001, 1001, 1002},
				{1001, 1001, 1002},
				{1001, 1002, 1003},
				{1001, 1002, 1003, 1004, 1005, 1006},
			},
			Flows: []int{2500, 2500, 1700, 1815},
		},
		{
			Num: 3,
			Groups: [][]int{
				{1001, 1001, 1002, 1002},
				{1001, 1001, 1002, 1003},
				{1001, 1001, 1002, 1003, 1004, 1005, 1006},
			},
			Flows: []int{3000, 2700, 2815},
		},
		{
			Num: 2,
			Groups: [][]int{
				{1001, 1001, 1001, 1002, 1002, 1003},
				{1001, 1001, 1001, 1002, 1002, 1003, 1004, 1005, 1006},
			},
			Flows: []int{4200, 4315},
		},
		{
			Num: 1,
			Groups: [][]int{
				{1001, 1001, 1001, 1001, 1001, 1001, 1002, 1002, 1002, 1002, 1003, 1003, 1004, 1005, 1006},
			},
			Flows: []int{8515},
		},
	}

	for _, c := range cases {
		draft := balancer.getBestDraft(fs, iders, c.Num)
		assert.Equal(t, c.Num, len(draft.Groups))

		flowsTotal := 0
		for i := 0; i < len(draft.Groups); i++ {
			assert.Equal(t, c.Groups[i], draft.Groups[i].IDs())
			assert.Equal(t, c.Flows[i], draft.Groups[i].Flows())
			flowsTotal += draft.Groups[i].Flows()
		}

		assert.Equal(t, total, flowsTotal)
	}
}

func TestDraftMultiZeroFlowCalculation(t *testing.T) {
	balancer := &autoBalancer{}

	fs, iders, total := generate([]flowWithCount{
		{Count: 6, DataID: 1001, Flow: 1000},
		{Count: 4, DataID: 1002, Flow: 500},
		{Count: 2, DataID: 1003, Flow: 200},
		{Count: 1, DataID: 1004, Flow: 80},
		{Count: 1, DataID: 1005, Flow: 30},
		{Count: 1, DataID: 1006, Flow: 5},
		{Count: 3, DataID: 1007, Flow: 0},
		{Count: 4, DataID: 1008, Flow: 0},
		{Count: 5, DataID: 1009, Flow: 0},
	})

	cases := []struct {
		Num    int
		Groups [][]int
		Flows  []int
		Zeros  map[int]int
	}{
		{
			Num: 3,
			Groups: [][]int{
				{1001, 1001, 1002, 1002},
				{1001, 1001, 1002, 1003},
				{1001, 1001, 1002, 1003, 1004, 1005, 1006},
			},
			Flows: []int{3000, 2700, 2815},
			Zeros: map[int]int{
				1007: 3,
				1008: 4,
				1009: 5,
			},
		},
		{
			Num: 2,
			Groups: [][]int{
				{1001, 1001, 1001, 1002, 1002, 1003},
				{1001, 1001, 1001, 1002, 1002, 1003, 1004, 1005, 1006},
			},
			Flows: []int{4200, 4315},
			Zeros: map[int]int{
				1007: 3,
				1008: 4,
				1009: 5,
			},
		},
	}

	for _, c := range cases {
		draft := balancer.getBestDraft(fs, iders, c.Num)
		assert.Equal(t, c.Num, len(draft.Groups))

		flowsTotal := 0
		for i := 0; i < len(draft.Groups); i++ {
			assert.Equal(t, c.Groups[i], draft.Groups[i].IDs())
			assert.Equal(t, c.Flows[i], draft.Groups[i].Flows())
			flowsTotal += draft.Groups[i].Flows()
		}

		assert.Equal(t, draft.zeroFlows, c.Zeros)
		assert.Equal(t, total, flowsTotal)
	}
}

func TestDraftMultiZeroFlowHash(t *testing.T) {
	balancer := &autoBalancer{}

	generateIders := func(n int) []define.IDer {
		iders := make([]define.IDer, 0)
		for i := 0; i < n; i++ {
			iders = append(iders, ider(i))
		}
		return iders
	}

	fs, iders, _ := generate([]flowWithCount{
		{Count: 6, DataID: 1001},
		{Count: 6, DataID: 1002},
		{Count: 6, DataID: 1003},
	})

	// case1: 6 nodes
	// 确保每个节点均会负载 3 个 dataid
	draft := balancer.getBestDraft(fs, iders, 6)
	solution := make(map[define.IDer][]define.IDer)
	balancer.handleZeroFlows(draft, generateIders(6), solution)
	for i := 0; i < 6; i++ {
		s := solution[ider(i)]
		sort.Slice(s, func(i, j int) bool { return s[i].ID() < s[j].ID() })
		assert.Equal(t, s, []define.IDer{
			ider(1001),
			ider(1002),
			ider(1003),
		})
	}

	// case2: 3 nodes
	// 确保每个节点均会负载 6 个 dataid
	draft = balancer.getBestDraft(fs, iders, 3)
	solution = make(map[define.IDer][]define.IDer)
	balancer.handleZeroFlows(draft, generateIders(3), solution)
	for i := 0; i < 3; i++ {
		s := solution[ider(i)]
		sort.Slice(s, func(i, j int) bool { return s[i].ID() < s[j].ID() })
		assert.Equal(t, s, []define.IDer{
			ider(1001),
			ider(1001),
			ider(1002),
			ider(1002),
			ider(1003),
			ider(1003),
		})
	}
}

func logIderMapWithFlow(printf func(format string, args ...interface{}), idFlows map[int][]define.IDer) {
	keys := make([]int, 0)
	for k := range idFlows {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	for _, k := range keys {
		var ids []int
		for _, item := range idFlows[k] {
			ids = append(ids, item.ID())
		}
		printf("	node=%v, items=%v", k, ids)
	}
}

func logIderMapAll(printf func(format string, args ...interface{}), idFlows map[define.IDer][]define.IDer) {
	keys := make([]define.IDer, 0)
	for k := range idFlows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].ID() < keys[j].ID()
	})

	for _, k := range keys {
		var ids []int
		for _, item := range idFlows[k] {
			ids = append(ids, item.ID())
		}
		printf("	node=%v, items=%v", k, ids)
	}
}

func testBalanceBoth(name string, t *testing.T, fixtureFlow []flowWithCount, node int, modify func([]flowWithCount)) {
	t.Logf("runing test: %s", name)
	items := mockItemIders(fixtureFlow)
	nodes := mockNodeIders(node)
	logging.SetLevel("info")

	hb := NewHashBalancer()
	iderMap1, _, _ := hb.Balance(define.NewPlanWithFlows(), items, nodes)
	plan := define.NewPlanWithFlows()
	plan.IDers = iderMap1
	plan.Flows = map[string]float64{}

	t.Log("[0]hash balancer.withflows")
	for k, v := range plan.IDers.All {
		t.Logf("	node=%v, items=%+v", k, v)
	}

	ab := NewAutoBalancer(0.3, 1, mockGetFlowsFunc(fixtureFlow), mockBalanceConfigFunc, "")
	iderMap2, _, _ := ab.Balance(plan, items, nodes)
	t.Log("[1]auto balancer.withflows")
	logIderMapWithFlow(t.Logf, iderMap2.WithFlow)
	t.Log("[1]auto balancer.all")
	logIderMapAll(t.Logf, iderMap2.All)

	plan = define.NewPlanWithFlows()
	plan.IDers = iderMap2
	modify(fixtureFlow)
	iderMap3, _, _ := ab.Balance(plan, mockItemIders(fixtureFlow), nodes)
	t.Log("[2]auto balancer.withflows")
	logIderMapWithFlow(t.Logf, iderMap3.WithFlow)
	t.Log("[2]auto balancer.all")
	logIderMapAll(t.Logf, iderMap3.All)
	t.Log() // newline
}

func TestBalanceBoth(t *testing.T) {
	testBalanceBoth("fixtureFlow1-1", t, fixtureFlow1(), 8, func(flows []flowWithCount) {
		for i := 0; i < len(flows); i++ {
			if flows[i].DataID == 526423 {
				flows[i].Count = 2
			}
		}
	})
	testBalanceBoth("fixtureFlow1-2", t, fixtureFlow1(), 8, func(flows []flowWithCount) {
		for i := 0; i < len(flows); i++ {
			if flows[i].DataID == 526423 {
				flows[i].Count = 3
			}
		}
	})
	testBalanceBoth("fixtureFlow1-3", t, fixtureFlow1(), 8, func(flows []flowWithCount) {
		for i := 0; i < len(flows); i++ {
			if flows[i].DataID == 526423 {
				flows[i].Count = 3
			}
		}
	})
	testBalanceBoth("fixtureFlow1-4", t, fixtureFlow1(), 8, func(flows []flowWithCount) {
		for i := 0; i < len(flows); i++ {
			if flows[i].DataID == 530112 {
				flows[i].Flow = 0
			}
		}
	})
}

func TestHugeDraftWithOverflow(t *testing.T) {
	fixtureFlows := [][]flowWithCount{
		fixtureFlow1(),
		fixtureFlow2(),
		fixtureFlow3(),
		fixtureFlow4(),
		fixtureFlow5(),
		fixtureFlow6(),
	}
	nodes := []int{6, 8, 10, 12, 14}
	for _, node := range nodes {
		for idx, fixtureFlow := range fixtureFlows {
			balancer := &autoBalancer{}
			fs, iders, _ := generate(fixtureFlow)
			draft := balancer.getBestDraft(fs, iders, node)

			sort.Slice(draft.Groups, func(i, j int) bool {
				return draft.Groups[i].Percent < draft.Groups[j].Percent
			})
			t.Logf("node:%d, flowIndex: %d, overflow: %v, maxRatio: %v", node, idx+1, draft.Overflow, draft.MaxRatio)
		}
	}
}
