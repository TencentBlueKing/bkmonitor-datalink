// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"sort"
	"strconv"
	"sync"
	"time"
)

type FlowRecorder struct {
	mut      sync.Mutex
	interval time.Duration
	curr     int
	prev     int
	done     bool
}

func NewFlowRecorder(interval time.Duration) *FlowRecorder {
	fr := &FlowRecorder{interval: interval}
	go fr.run()

	return fr
}

func (fr *FlowRecorder) Add(n int) {
	fr.mut.Lock()
	defer fr.mut.Unlock()

	fr.curr += n
}

func (fr *FlowRecorder) Get() int {
	return fr.prev
}

func (fr *FlowRecorder) Stop() {
	fr.done = true
}

func (fr *FlowRecorder) run() {
	seconds := int64(fr.interval.Seconds())
	for {
		if fr.done {
			return
		}

		// 确保每个 pipeline 拿到的都是同一个时间点的数据
		if time.Now().Unix()%seconds == 0 {
			fr.mut.Lock()
			fr.prev = fr.curr
			fr.curr = 0
			fr.mut.Unlock()
		}
		time.Sleep(time.Second)
	}
}

type FlowItem struct {
	DataID  int
	Cluster string
	Type    string
	Service string
	Path    string
	Flow    int
}

const (
	FlowItemKeyDataID  = "dataid"
	FlowItemKeyService = "service"
	FlowItemKeyType    = "type"
)

type FlowItems []FlowItem

func (fi FlowItems) Len() int {
	return len(fi)
}

func (fi FlowItems) Less(i, j int) bool {
	return fi[i].Flow > fi[j].Flow
}

func (fi FlowItems) Swap(i, j int) {
	fi[i], fi[j] = fi[j], fi[i]
}

func (fi FlowItems) GroupBy(identifier string, filters ...string) map[string]FlowItems {
	return fi.groupBy(identifier, filters...)
}

func (fi FlowItems) SumBy(identifier string) map[string]int {
	return fi.sumBy(fi.groupBy(identifier))
}

func (fi FlowItems) AvgBy(identifier string) map[string]int {
	return fi.avgBy(fi.groupBy(identifier))
}

func (fi FlowItems) SumPercentBy(identifier string) map[string]float64 {
	return fi.sumPercentBy(fi.groupBy(identifier))
}

func (fi FlowItems) isIn(slices []string, a string) bool {
	for _, slice := range slices {
		if slice == a {
			return true
		}
	}

	return false
}

func (fi FlowItems) groupBy(identifier string, filters ...string) map[string]FlowItems {
	ret := make(map[string]FlowItems)
	for _, item := range fi {
		var key string
		switch identifier {
		case FlowItemKeyDataID:
			key = strconv.Itoa(item.DataID)
		case FlowItemKeyType:
			key = item.Type
		default: // FlowItemKeyService
			key = item.Service
		}

		if len(filters) == 0 || fi.isIn(filters, key) {
			if _, ok := ret[key]; !ok {
				ret[key] = make([]FlowItem, 0)
			}
			ret[key] = append(ret[key], item)
		}
	}

	for k := range ret {
		sort.Sort(ret[k])
	}
	return ret
}

func (fi FlowItems) sumBy(m map[string]FlowItems) map[string]int {
	ret := make(map[string]int)
	for k, v := range m {
		for _, item := range v {
			ret[k] += item.Flow
		}
	}

	return ret
}

func (fi FlowItems) avgBy(m map[string]FlowItems) map[string]int {
	type R struct {
		flow  int
		count int
	}

	ret := make(map[string]*R)
	for k, v := range m {
		ret[k] = &R{}
		for _, item := range v {
			ret[k].count += 1
			ret[k].flow += item.Flow
		}
	}

	data := make(map[string]int)
	for k, v := range ret {
		data[k] = v.flow / v.count
	}

	return data
}

func (fi FlowItems) sumPercentBy(m map[string]FlowItems) map[string]float64 {
	ret := make(map[string]float64)
	var total float64
	for k, v := range m {
		for _, item := range v {
			total += float64(item.Flow)
			ret[k] += float64(item.Flow)
		}
	}

	for k := range ret {
		if total == 0 {
			ret[k] = 0
			continue
		}
		ret[k] /= total
	}

	return ret
}
