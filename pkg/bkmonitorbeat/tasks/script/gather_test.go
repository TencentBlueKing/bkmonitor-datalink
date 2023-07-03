// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package script

import (
	"context"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

type OutPutItem struct {
	expectedErrCode define.BeatErrorCode
	expectedOut     []common.MapStr
}

type TestItem struct {
	name   string
	input  string
	output OutPutItem
}

func newGather() *Gather {
	globalConf := configs.NewConfig()
	taskConf := configs.NewScriptTaskConfig()
	err := globalConf.Clean()
	if err != nil {

	}
	err = taskConf.Clean()
	if err != nil {

	}
	taskConf.TimeOffset = time.Hour * 24 * 365 * 10

	return New(globalConf, taskConf).(*Gather)
}

func TestGather_KeepOneDimension(t *testing.T) {
	gather := newGather()
	globalConfig := gather.GlobalConfig.(*configs.Config)
	globalConfig.KeepOneDimension = true

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCase := []TestItem{
		{
			input: `sys_disk_size{mountpoint="/usr/local"} 8.52597957704355
					sys_disk_size{mountpoint="/data"} 90.1
					sys_disk_size{mountpoint="/"} 23.52
					sys_disk_used{mountpoint="/usr/local"} 25.52
					sys_disk_used{mountpoint="/data"} 2.52
					sys_disk_used{mountpoint="/"} 93.52
					sys_device{devicename="vda"} 1
					sys_device{devicename="vdb"} 2
					sys_device{devicename="vdc"} 3
					sys_net{devicename="lo"} 123
					sys_net{devicename="eth0",supperlier="Dell"} 456`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"dimensions": common.MapStr{"mountpoint": "/usr/local"}, "metrics": common.MapStr{"sys_disk_size": float64(8.52597957704355), "sys_disk_used": float64(25.52)}, "catched": 0},
					{"dimensions": common.MapStr{"devicename": "vda"}, "metrics": common.MapStr{"sys_device": float64(1)}, "catched": 0},
					{"dimensions": common.MapStr{"devicename": "lo"}, "metrics": common.MapStr{"sys_net": float64(123)}, "catched": 0},
					{"dimensions": common.MapStr{"devicename": "eth0", "supperlier": "Dell"}, "metrics": common.MapStr{"sys_net": float64(456)}, "catched": 0},
				},
			},
		},
	}
	checkResult(t, gather, testCase)
}

// TestMultiScriptGather 预期行为：输出多行且去重
func TestGather_Run(t *testing.T) {
	gather := newGather()

	//event := script.NewEvent(gather)
	//event.ScriptFail(define.BeatErrScriptFormatOutputError, "failed")
	//errOutputEvent := event.AsMapStr()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCase := []TestItem{
		{
			name:  "用例一、单条数据，预期情况为正常输出",
			input: `sys_disk_size{mountpoint="/usr/local"} 8.52597957704355`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"dimensions": common.MapStr{"mountpoint": "/usr/local"}, "metrics": common.MapStr{"sys_disk_size": float64(8.52597957704355)}, "catched": 0},
				},
			},
		},
		{
			name: "用例二、多条数据，包括重复数据，预期情况为去重",
			input: `test1{L0="123",L1="45ee"} 1234
		test1{L0="124",L1="45ed"} 12345
		test2{T1="aa",T2="bb"} 1234
		test1{L0="123",L1="45ee"} 1234
		test2{T1="aa",T2="bb"} 1234555`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"dimensions": common.MapStr{"L0": "123", "L1": "45ee"}, "metrics": common.MapStr{"test1": float64(1234)}, "catched": 0},
					{"dimensions": common.MapStr{"L0": "124", "L1": "45ed"}, "metrics": common.MapStr{"test1": float64(12345)}, "catched": 0},
					{"dimensions": common.MapStr{"T1": "aa", "T2": "bb"}, "metrics": common.MapStr{"test2": float64(1234555)}, "catched": 0},
				},
			},
		},
		{
			name: "用例三、第一条为错误数据(label没有双引号)，预期情况为错误数据不输出，正确数据输出",
			input: `test1{L0="123",L1=45ee} 1234
			test1{L0="124",L1="45ed"} 12345`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"dimensions": common.MapStr{"L0": "124", "L1": "45ed"}, "metrics": common.MapStr{"test1": float64(12345)}, "catched": 0},
				},
			},
		},
		{
			name:  "用例四、label之间有空格的数据，预期结果为正常输出",
			input: `abctest{t1="123", t2="456"} 181818`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"abctest": float64(181818)}, "catched": 0},
				},
			},
		},
		{
			name: "用例五、第一行数据大括号不封闭，预期结果为第一行数据不输出",
			input: `abctest{t1="123", t2="456" 181818
			test1{L0="124",L1="45ed"} 12345`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"dimensions": common.MapStr{"L0": "124", "L1": "45ed"}, "metrics": common.MapStr{"test1": float64(12345)}, "catched": 0},
				},
			},
		},
		{
			name: "用例六、两个数据label相同且时间戳相同，但metric_name不同,预期被合并为同一条数据输出",
			input: `t1{t1="123", t2="456"} 181818
		t2{t1="123",t2="456"} 12345`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(181818), "t2": float64(12345)}, "catched": 0},
				},
			},
		},
		{
			name: "用例七、两个数据只有时间戳不同，也会被区分为两条不同的数据",
			input: `t1{t1="123", t2="456"} 181818 1583137100
		t1{t1="123",t2="456"} 181818 1583138100`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"time": int64(1583137100), "dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(181818)}, "catched": 0},
					{"time": int64(1583138100), "dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(181818)}, "catched": 0},
				},
			},
		},
		{
			name: "用例七、两个数据只有时间戳不同，为毫秒级时间戳",
			input: `t1{t1="123", t2="456"} 32576 1584137100000
					t1{t1="123", t2="456"} 32576 1584138100000`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"time": int64(1584137100), "dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(32576)}, "catched": 0},
					{"time": int64(1584138100), "dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(32576)}, "catched": 0},
				},
			},
		},
		{
			name: "用例八、场景混合",
			input: `sys_disk_size{mountpoint="/usr/local"} 8.52597957704355
			test1{L0="123",L1="45ee"} 1234
			test1{L0="124",L1="45ed"} 12345
			test2{T1="aa",T2="bb"} 1234
			test1{L0="123",L1="45ee"} 1234
			abctest{t1="123", t2="456" } 181818
			test2{T1="aa", T2="bb"} 1234555
			t1{t1="123", t2="456"} 181818 1583137100
			t1{t1="123", t2="456"} 181818 1583138100
			t1{t1="123", t2="456"} 32576 1584137100000
			t1{t1="123", t2="456"} 32576 1584138100000`,
			output: OutPutItem{
				expectedErrCode: define.BeatErrCodeOK,
				expectedOut: []common.MapStr{
					{"dimensions": common.MapStr{"mountpoint": "/usr/local"}, "metrics": common.MapStr{"sys_disk_size": float64(8.52597957704355)}, "catched": 0},
					{"dimensions": common.MapStr{"L0": "123", "L1": "45ee"}, "metrics": common.MapStr{"test1": float64(1234)}, "catched": 0},
					{"dimensions": common.MapStr{"L0": "124", "L1": "45ed"}, "metrics": common.MapStr{"test1": float64(12345)}, "catched": 0},
					{"dimensions": common.MapStr{"T1": "aa", "T2": "bb"}, "metrics": common.MapStr{"test2": float64(1234555)}, "catched": 0},
					{"dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"abctest": float64(181818)}, "catched": 0},
					{"time": int64(1583137100), "dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(181818)}, "catched": 0},
					{"time": int64(1583138100), "dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(181818)}, "catched": 0},
					{"time": int64(1584137100), "dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(32576)}, "catched": 0},
					{"time": int64(1584138100), "dimensions": common.MapStr{"t1": "123", "t2": "456"}, "metrics": common.MapStr{"t1": float64(32576)}, "catched": 0},
				},
			},
		},
	}

	checkResult(t, gather, testCase)
}

func checkResult(t *testing.T, gather *Gather, testCase []TestItem) {
	tempData := ""
	stubs := gostub.Stub(&ExecCmdLine, func(ctx context.Context, arg string, userEnvs map[string]string) (string, error) {
		return tempData, nil
	})
	defer stubs.Reset()

	for itemIndex, item := range testCase {
		t.Run(item.name, func(t *testing.T) {
			tempData = item.input
			outputResult := item.output
			e := make(chan define.Event, 1)
			go func() {
				gather.Run(context.Background(), e)
				gather.Wait()
				// gather运行完就关闭通道，这样下面的for循环才能读取完数据后退出
				close(e)
			}()

			eventCount := 0
			for event := range e {
				if event == nil {
					t.Errorf("run task error")
				}

				eventCount++
				ms := event.AsMapStr()
				var code = ms["error_code"].(define.BeatErrorCode)
				if code != outputResult.expectedErrCode /* || ms["message"] != "success" */ {
					t.Errorf("item:%d, script event result failed, excepted code(%d), result code(%d)", itemIndex, outputResult.expectedErrCode, code)
				} else {
					// 如果是正常输出，需要继续比较结果
					if code == define.BeatErrCodeOK {
						compareEvent(ms, outputResult.expectedOut)
					}
				}
			}
			if eventCount != len(outputResult.expectedOut) {
				t.Errorf("item:%d, not expected, return event count(%d), expected count(%d)",
					itemIndex, eventCount, len(outputResult.expectedOut))
			}

			if outputResult.expectedErrCode == define.BeatErrCodeOK {
				for resultIndex, result := range outputResult.expectedOut {
					if result["catched"] != 1 {
						t.Errorf("item:%d,idx:%d,catched not as expected", itemIndex, resultIndex)
					}
				}
			}
		})
	}
}

func compareEvent(eventMap common.MapStr, expectedMapList []common.MapStr) {
	dimensions := eventMap["dimensions"].(common.MapStr)
	metrics := eventMap["metrics"].(common.MapStr)
	for _, expectedMap := range expectedMapList {
		expectedDimensions := expectedMap["dimensions"].(common.MapStr)
		expectedMetrics := expectedMap["metrics"].(common.MapStr)
		if compareMapStr(dimensions, expectedDimensions) && compareMapStr(metrics, expectedMetrics) {
			// 时间戳指定，且对不上
			if _, ok := expectedMap["time"]; ok && expectedMap["time"] != eventMap["time"] {
				continue
			}
			expectedMap["catched"] = expectedMap["catched"].(int) + 1

		}
	}
}

// 两个map进行比较，2包含1则为true
func compareMapStr(mapStr1 common.MapStr, mapStr2 common.MapStr) bool {
	for key2, value2 := range mapStr2 {
		catched := false
		for key1, value1 := range mapStr1 {
			if key1 == key2 && value1 == value2 {
				catched = true
			}
		}
		if !catched {
			return false
		}
	}
	return true
}
