// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mapping

import (
	"fmt"
	"sync"
)

// Process :
type Process struct {
	pid    int
	name   string
	params string
}

// NewProcess :
func NewProcess(pid int, name string, params string) Process {
	return Process{pid, name, params}
}

// Operator 独立的映射处理器
type Operator struct {
	placeHolder  string
	preGlobalMap map[string][]int
	prePIDList   []Process
	lock         *sync.RWMutex
}

// NewOperator :
func NewOperator() *Operator {
	return &Operator{
		placeHolder:  "##BK##",
		preGlobalMap: make(map[string][]int),
		prePIDList:   make([]Process, 0),
		lock:         new(sync.RWMutex),
	}
}

// GetMappingPID 根据传入的进程信息，获取一个映射pid
func (m *Operator) GetMappingPID(proc Process) int {
	m.lock.RLock()
	defer m.lock.RUnlock()

	group := m.getProcessGroup(proc)
	// 如果查的到，尝试直接获取对应pid映射位
	if mapping, ok := m.preGlobalMap[group]; ok {
		for mappingPID, pid := range mapping {
			if proc.pid == pid {
				return mappingPID
			}
		}
	}

	// 否则返回-1，表示匹配失败
	return -1
}

// RefreshGlobalMap 根据进程对比，刷新全局map,并返回对应的pid映射关系
func (m *Operator) RefreshGlobalMap(currList []Process) {
	m.lock.Lock()
	defer m.lock.Unlock()

	currGlobalMap := make(map[string][]int)
	missingCurrentProcess := make([]Process, 0)
	pidRepeatMap := make(map[string]bool)
	// 分组，目标是存储各分组的进程长度
	groupLength := make(map[string]int)
	for _, proc := range currList {
		// 由于存在重复上报pid的情况，所以这里进行去重，以避免单个进程占用多个占位
		ident := m.getIndent(proc)
		if _, ok := pidRepeatMap[ident]; ok {
			continue
		} else {
			pidRepeatMap[ident] = true
		}
		group := m.getProcessGroup(proc)
		if _, ok := groupLength[group]; !ok {
			groupLength[group] = 0
		}
		groupLength[group]++
	}

	pidRepeatMap = make(map[string]bool)
	// 获取连续上报的数据，直接继承上次上报的映射pid
	for _, proc := range currList {
		// 由于存在重复上报pid的情况，所以这里进行去重，以避免单个进程占用多个占位
		ident := m.getIndent(proc)
		if _, ok := pidRepeatMap[ident]; ok {
			continue
		} else {
			pidRepeatMap[ident] = true
		}
		matched := false
		for _, preProc := range m.prePIDList {
			// 匹配二者，匹配上的说明是是连续上报状态的数据
			if m.matchProc(preProc, proc) {
				matched = true
				// 此时将这个进程信息同步到currGlobalMap中
				m.inherateProcessInfo(proc, groupLength, m.preGlobalMap, currGlobalMap)
				break
			}
		}
		// 未能匹配到旧列表进程的记录下来
		if !matched {
			missingCurrentProcess = append(missingCurrentProcess, proc)
		}
	}

	// 处理未能匹配的进程序列
	for _, proc := range missingCurrentProcess {
		group := m.getProcessGroup(proc)
		innerCurrList := currGlobalMap[group]
		inserted := false
		for index, pid := range innerCurrList {
			// pid为0说明是空位
			if pid == 0 {
				innerCurrList[index] = proc.pid
				inserted = true
				break
			}
		}
		// 没插入到说明没空位，此时要append列表了
		if !inserted {
			innerCurrList = append(innerCurrList, proc.pid)
			currGlobalMap[group] = innerCurrList
		}

	}
	m.preGlobalMap = currGlobalMap
	m.prePIDList = currList
}

// 上报的映射pid
func (m *Operator) getProcessGroup(proc Process) string {
	path := proc.name + m.placeHolder + proc.params
	return path
}

// 确认两个进程是否匹配
func (m *Operator) matchProc(preProc, currProc Process) bool {
	if preProc.pid == currProc.pid && preProc.name == currProc.name &&
		preProc.params == currProc.params {
		return true
	}
	return false
}

func (m *Operator) maxIngeger(num1, num2 int) int {
	if num1 > num2 {
		return num1
	}
	return num2
}

func (m *Operator) getIndent(proc Process) string {
	format := "%d" + m.placeHolder + "%s" + m.placeHolder + "%s"
	return fmt.Sprintf(format, proc.pid, proc.name, proc.params)
}

func (m *Operator) inherateProcessInfo(proc Process, groupLength map[string]int, preGlobalMap, currentGlobalMap map[string][]int) {
	group := m.getProcessGroup(proc)
	// 没有二级map就初始化一个
	if _, ok := currentGlobalMap[group]; !ok {
		currentGlobalMap[group] = make([]int, m.maxIngeger(groupLength[group], len(preGlobalMap[group])))
	}
	for mappingPID, pid := range preGlobalMap[group] {
		// 继承之前的mapping定位
		if pid == proc.pid {
			currentGlobalMap[group][mappingPID] = pid
		}
	}
}

// GenerateReportList 用全局map生成没有分组的pid清单
func (m *Operator) GenerateReportList() map[string]map[int]int {
	currentGlobalMap := m.preGlobalMap
	resultMap := make(map[string]map[int]int)
	for group, outer := range currentGlobalMap {
		groupMap := make(map[int]int)
		for mappingPID, pid := range outer {
			if pid != 0 {
				groupMap[pid] = mappingPID
			}
		}
		resultMap[group] = groupMap
	}
	return resultMap
}
