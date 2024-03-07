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
	"testing"
)

func TestMapping(t *testing.T) {
	// 重置两个全局变量，开始测试
	operator := NewOperator()

	// 第一次采集到的进程
	currList1 := []Process{
		{
			1, "test", "233",
		},
		{
			1, "test", "233",
		},
		{
			1, "test", "233",
		},
		{
			2, "test", "233",
		},
		{
			3, "test", "233",
		},
		{
			3, "test", "234",
		},
		{
			4, "test1", "",
		},
		{
			20, "test1", "",
		},
	}
	// 第一次期望获得的pid映射
	expectedResult1 := map[string]map[int]int{
		"test##BK##233": {
			1: 0,
			2: 1,
			3: 2,
		},
		"test##BK##234": {
			3: 0,
		},
		"test1##BK##": {
			4:  0,
			20: 1,
		},
	}

	// 第二次采集到的进程，第二、三个进程发生了重启
	currList2 := []Process{
		{
			1, "test", "233",
		},
		{
			5, "test", "233",
		},
		{
			6, "test", "233",
		},
		{
			4, "test1", "",
		},
		{
			20, "test1", "",
		},
	}
	// 第二次期望获得的pid映射
	expectedResult2 := map[string]map[int]int{
		"test##BK##233": {
			1: 0,
			5: 1,
			6: 2,
		},
		"test1##BK##": {
			4:  0,
			20: 1,
		},
	}

	// 第三次采集到的进程，第一个进程消失
	currList3 := []Process{
		{
			5, "test", "233",
		},
		{
			6, "test", "233",
		},
		{
			4, "test1", "",
		},
		{
			20, "test1", "",
		},
	}
	// 第三次期望获得的pid映射
	expectedResult3 := map[string]map[int]int{
		"test##BK##233": {
			5: 1,
			6: 2,
		},
		"test1##BK##": {
			4:  0,
			20: 1,
		},
	}

	// 第四次采集到的进程
	// 第一个进程重新出现，且占用了原pid2
	// 新增一个新进程
	currList4 := []Process{
		{
			2, "test", "233",
		},
		{
			5, "test", "233",
		},
		{
			6, "test", "233",
		},
		{
			4, "test1", "",
		},
		{
			20, "test1", "",
		},
		{
			21, "test2", "",
		},
	}
	// 第四次期望获得的pid映射
	expectedResult4 := map[string]map[int]int{
		"test##BK##233": {
			2: 0,
			5: 1,
			6: 2,
		},
		"test1##BK##": {
			4:  0,
			20: 1,
		},
		"test2##BK##": {
			21: 0,
		},
	}

	// 第五次采集到的进程
	// test进程多了一个使用其他参数的实例
	currList5 := []Process{
		{
			2, "test", "233",
		},
		{
			5, "test", "233",
		},
		{
			6, "test", "233",
		},
		{
			7, "test", "234",
		},
		{
			4, "test1", "",
		},
		{
			20, "test1", "",
		},
		{
			21, "test2", "",
		},
	}
	// 第五次期望获得的pid映射
	expectedResult5 := map[string]map[int]int{
		"test##BK##233": {
			2: 0,
			5: 1,
			6: 2,
		},
		"test##BK##234": {
			7: 0,
		},
		"test1##BK##": {
			4:  0,
			20: 1,
		},
		"test2##BK##": {
			21: 0,
		},
	}

	// 第六次采集到的进程
	// 无事发生
	currList6 := []Process{
		{
			2, "test", "233",
		},
		{
			5, "test", "233",
		},
		{
			6, "test", "233",
		},
		{
			7, "test", "234",
		},
		{
			4, "test1", "",
		},
		{
			20, "test1", "",
		},
		{
			21, "test2", "",
		},
	}
	// 第六次期望获得的pid映射
	expectedResult6 := map[string]map[int]int{
		"test##BK##233": {
			2: 0,
			5: 1,
			6: 2,
		},
		"test##BK##234": {
			7: 0,
		},
		"test1##BK##": {
			4:  0,
			20: 1,
		},
		"test2##BK##": {
			21: 0,
		},
	}

	testList := [][]Process{
		currList1,
		currList2,
		currList3,
		currList4,
		currList5,
		currList6,
	}
	expectMapList := []map[string]map[int]int{
		expectedResult1,
		expectedResult2,
		expectedResult3,
		expectedResult4,
		expectedResult5,
		expectedResult6,
	}

	for index, currList := range testList {
		expectedResult := expectMapList[index]
		operator.RefreshGlobalMap(currList)
		resultMap := operator.GenerateReportList()
		if len(expectedResult) != len(resultMap) {
			t.Errorf("index:%d,result mapping count not as expected,expedted:%d,acutal:%d", index+1, len(expectedResult), len(resultMap))
		}
		for group, expectedGroupMap := range expectedResult {
			groupMap, ok := resultMap[group]
			if !ok {
				t.Errorf("index:%d,get group map failed,group name:%s", index, group)
				continue
			}
			if len(groupMap) != len(expectedGroupMap) {
				t.Errorf("index:%d,groupMap:%s length not match,expected:%d,actual:%d", index, group, len(expectedGroupMap), len(groupMap))
			}
			for expectedPID, expectedMappingPID := range expectedGroupMap {
				if mappedPID, ok := groupMap[expectedPID]; !ok {
					t.Errorf("index:%d,get mapping pid failed,group:%s,pid:%d", index, group, expectedPID)
					continue
				} else {
					if mappedPID != expectedMappingPID {
						t.Errorf("index:%d,group:%s,pid:%d,mapping pid not match,expected:%d,actual:%d", index, group, expectedPID, expectedMappingPID, mappedPID)
					}
				}
			}

		}
	}
}
