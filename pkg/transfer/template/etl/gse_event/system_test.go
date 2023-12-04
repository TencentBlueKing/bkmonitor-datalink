// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package gse_event

import (
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/standard"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

type SystemEventSuite struct {
	StoreSuite
}

func (s *SystemEventSuite) runCase(input string, pass bool, dimensions map[string]interface{}, outputCount int) {
	hostInfo := models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
		CCTopoBaseModelInfo: &models.CCTopoBaseModelInfo{
			BizID: []int{2},
			Topo:  []map[string]string{},
		},
	}
	agentHostInfo := models.CCAgentHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
		AgentID: "demo",
		BizID:   2,
	}
	s.StoreHost(&hostInfo).AnyTimes()
	s.StoreAgentHost(&agentHostInfo).AnyTimes()

	s.Store.EXPECT().Get(gomock.Any()).Return(nil, define.ErrItemNotFound).AnyTimes()

	t := s.T()
	payload := define.NewJSONPayloadFrom([]byte(input), 0)

	var wg sync.WaitGroup

	outputChan := make(chan define.Payload)
	killChan := make(chan error)

	wg.Add(1)
	go func() {
		for err := range killChan {
			panic(err)
		}
		wg.Done()
	}()

	processor := NewSystemEventProcessor(s.CTX, "test")
	go func() {
		processor.Process(payload, outputChan, killChan)
		close(killChan)
		close(outputChan)
	}()

	t.Log(input)
	for output := range outputChan {
		s.True(pass)
		outputCount--
		var record standard.EventRecord
		s.NoError(output.To(&record))
		if !cmp.Equal(dimensions, record.EventDimension) {
			diff := cmp.Diff(dimensions, record.EventDimension)
			s.FailNow("dimensions differ: %#v", diff)
		}
	}

	if outputCount != 0 {
		s.FailNow("output count not match")
	}

	wg.Wait()
}

// TestUsage :
func (s *SystemEventSuite) TestUsage() {
	cases := []struct {
		input       string
		pass        bool
		dimensions  map[string]interface{}
		outputCount int
	}{
		{`{}`, false, nil, 0},
		// 测试正常的输入内容
		{
			`{
				"server": "",
				"time": "2019-03-02 15:29:24",
				"timezone": 0,
				"utctime": "2019-03-02 15:29:24",
				"utctime2": "2019-03-02 07:29:24",
				"value": [
					{
						"event_desc": "",
						"event_raw_id": 0,
						"event_time": "2019-03-02 07:29:24",
						"event_source_system": "",
						"event_title": "",
						"event_type": "gse_basic_alarm_type",
						"extra": {
							"type": 2,
							"count": 0,
							"host": [
								{
									"bizid": 0,
									"agent_id": "demo"
								}
							]
						}
					}
				]
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"bk_target_cloud_id": "0",
				"bk_target_ip":       "127.0.0.1",
				"ip":                 "127.0.0.1",
				"bk_cloud_id":        "0",
				"bk_biz_id":          "2",
				"bk_agent_id":        "demo",
			},
			1,
		},
		{
			`{
				"server": "",
				"time": "2019-03-02 15:29:24",
				"timezone": 0,
				"utctime": "2019-03-02 15:29:24",
				"utctime2": "2019-03-02 07:29:24",
				"value": [
					{
						"event_desc": "",
						"event_raw_id": 0,
						"event_time": "2019-03-02 07:29:24",
						"event_source_system": "",
						"event_title": "",
						"event_type": "gse_basic_alarm_type",
						"extra": {
							"type": 2,
							"count": 0,
							"host": [
								{
									"bizid": 0,
									"agent_id": "0:127.0.0.1"
								}
							]
						}
					}
				]
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"bk_target_cloud_id": "0",
				"bk_target_ip":       "127.0.0.1",
				"ip":                 "127.0.0.1",
				"bk_cloud_id":        "0",
				"bk_biz_id":          "2",
			},
			1,
		},
		{
			`{
				"server": "",
				"time": "2019-03-02 15:29:24",
				"timezone": 0,
				"utctime": "2019-03-02 15:29:24",
				"utctime2": "2019-03-02 07:29:24",
				"value": [
					{
						"event_desc": "",
						"event_raw_id": 0,
						"event_time": "2019-03-02 07:29:24",
						"event_source_system": "",
						"event_title": "",
						"event_type": "gse_basic_alarm_type",
						"extra": {
							"type": 2,
							"count": 0,
							"host": [
								{
									"bizid": 0,
                                    "cloudid": 0,
									"ip": "127.0.0.1"
								}
							]
						}
					}
				]
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"bk_target_cloud_id": "0",
				"bk_target_ip":       "127.0.0.1",
				"ip":                 "127.0.0.1",
				"bk_cloud_id":        "0",
				"bk_biz_id":          "2",
			},
			1,
		},
		{
			`{
				"server": "",
				"time": "2019-03-02 15:29:24",
				"timezone": 0,
				"utctime": "2019-03-02 15:29:24",
				"utctime2": "2019-03-02 07:29:24",
				"value": [
					{
						"event_desc": "",
						"event_raw_id": 0,
						"event_time": "2019-03-02 07:29:24",
						"event_source_system": "",
						"event_title": "",
						"event_type": "gse_basic_alarm_type",
						"extra": {
							"type": 2,
							"count": 0,
							"host": [
								{
									"bizid": 0,
                                    "agend_id": "nd181js91"
								}
							]
						}
					}
				]
			}`,
			false,
			// dimensions
			map[string]interface{}{},
			0,
		},
		{
			`{
				"isdst":0,
				"server":"127.0.0.129",
				"time":"2018-03-01 11:45:42",
				"timezone":8,
				"utctime":"2018-03-01 11:45:42",
				"utctime2":"2018-03-01 03:45:42",
				"value":[
					{
						"event_desc":"",
						"event_raw_id":11,
						"event_source_system":"",
						"event_time":"2018-03-01 11:45:42",
						"event_title":"",
						"event_type":"gse_basic_alarm_type",
						"extra":{
							"bizid":0,
							"cloudid":0,
							"executable": "test",
							"executable_path": "/tmp/test",
							"signal":"SIGFPE",
							"corefile":"/data/corefile/core_101041_2018-03-10",
							"filesize":"0",
							"host":"127.0.0.1",
							"type":7
						}
					}
				]
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"bk_target_cloud_id": "0",
				"bk_target_ip":       "127.0.0.1",
				"ip":                 "127.0.0.1",
				"bk_cloud_id":        "0",
				"bk_biz_id":          "2",
				"executable":         "test",
				"executable_path":    "/tmp/test",
				"signal":             "SIGFPE",
				"corefile":           "/data/corefile/core_101041_2018-03-10",
				"filesize":           "0",
			},
			1,
		},
		{
			`{
				"isdst":0,
				"utctime2":"2019-10-17 05:53:53",
				"value":[
					{
						"event_raw_id":7795,
						"event_type":"gse_basic_alarm_type",
						"event_time":"2019-10-17 13:53:53",
						"extra":{
							"used_percent":93,
							"used":45330684,
							"cloudid":0,
							"free":7,
							"fstype":"ext4",
							"host":"127.0.0.1",
							"disk":"/",
							"file_system":"/dev/vda1",
							"size":51473888,
							"bizid":0,
							"avail":3505456,
							"type":6
						},
						"event_title":"",
						"event_desc":"",
						"event_source_system":""
					}
				],
				"server":"127.0.0.129",
				"utctime":"2019-10-17 13:53:53",
				"time":"2019-10-17 13:53:53",
				"timezone":8
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"bk_target_cloud_id": "0",
				"bk_target_ip":       "127.0.0.1",
				"ip":                 "127.0.0.1",
				"bk_cloud_id":        "0",
				"bk_biz_id":          "2",
				"disk":               "/",
				"file_system":        "/dev/vda1",
				"fstype":             "ext4",
			},
			1,
		},
		{
			`{
				"isdst":0,
				"utctime2":"2019-10-16 00:28:53",
				"value":[
					{
						"event_raw_id":5853,
						"event_type":"gse_basic_alarm_type",
						"event_time":"2019-10-16 08:28:53",
						"extra":{
							"cloudid":0,
							"host":"127.0.0.1",
							"ro":[
								{
									"position":"/sys/fs/cgroup",
									"fs":"tmpfs",
									"type":"tmpfs"
								}
							],
							"type":3,
							"bizid":0
						},
						"event_title":"",
						"event_desc":"",
						"event_source_system":""
					}
				],
				"server":"127.0.0.1",
				"utctime":"2019-10-16 08:28:53",
				"time":"2019-10-16 08:28:53",
				"timezone":8
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"bk_target_cloud_id": "0",
				"bk_target_ip":       "127.0.0.1",
				"ip":                 "127.0.0.1",
				"bk_cloud_id":        "0",
				"bk_biz_id":          "2",
				"position":           "/sys/fs/cgroup",
				"fs":                 "tmpfs",
				"type":               "tmpfs",
			},
			1,
		},
		{
			`{
				"isdst":0,
				"server":"127.0.0.129",
				"time":"2018-03-01 11:45:42",
				"timezone":8,
				"utctime":"2018-03-01 11:45:42",
				"utctime2":"2018-03-01 03:45:42",
				"value":[
					{
						"event_desc":"",
						"event_raw_id":11,
						"event_source_system":"",
						"event_time":"2018-03-01 11:45:42",
						"event_title":"",
						"event_type":"gse_basic_alarm_type",
						"extra":{
							"bizid":0,
							"cloudid":0,
							"host":"127.0.0.1",
							"type":9,
							"total":3,
							"process":"oom/java/consul",
							"message":"total-vm:44687536kB, anon-rss:32520504kB, file-rss:0kB, shmem-rss:0kB",
							"oom_memcg" : "oom_cgroup_path",
							"task_memcg" :  "oom_cgroup_task",
							"task" :  "process_name",
							"constraint" :  "CONSTRAINT_MEMCG"
						}
					}
				]
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"bk_target_cloud_id": "0",
				"bk_target_ip":       "127.0.0.1",
				"ip":                 "127.0.0.1",
				"bk_cloud_id":        "0",
				"bk_biz_id":          "2",
				"process":            "oom/java/consul",
				"message":            "total-vm:44687536kB, anon-rss:32520504kB, file-rss:0kB, shmem-rss:0kB",
				"oom_memcg":          "oom_cgroup_path",
				"task_memcg":         "oom_cgroup_task",
				"task":               "process_name",
				"constraint":         "CONSTRAINT_MEMCG",
			},
			1,
		},
		{
			`{
				"server":"127.0.0.1",
				"time":"2019-10-15 17:34:44",
				"value":[
					{
						"event_desc":"",
						"event_raw_id":27422,
						"event_source_system":"",
						"event_time":"2019-10-15 09:34:44",
						"event_timezone":0,
						"event_title":"",
						"event_type":"gse_basic_alarm_type",
						"extra":{
							"bizid":0,
							"cloudid":0,
							"count":30,
							"host":"127.0.0.1",
							"iplist":["127.0.0.1"],
							"type":8
						}
					}
				]
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"bk_target_cloud_id": "0",
				"bk_target_ip":       "127.0.0.1",
				"ip":                 "127.0.0.1",
				"bk_cloud_id":        "0",
				"bk_biz_id":          "2",
			},
			1,
		},
	}
	for _, c := range cases {
		s.runCase(c.input, c.pass, c.dimensions, c.outputCount)
	}
}

func TestSystemEventSuite(t *testing.T) {
	suite.Run(t, new(SystemEventSuite))
}
