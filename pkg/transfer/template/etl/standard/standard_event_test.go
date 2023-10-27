// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package standard_test

import (
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/standard"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// ProcessSuite :
type EventProcessSuite struct {
	StoreSuite
}

func (s *EventProcessSuite) runCase(input string, pass bool, dimensions map[string]interface{}, metrics map[string]interface{}) {
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

	processor := standard.NewEventProcessor(s.CTX, "test")
	go func() {
		processor.Process(payload, outputChan, killChan)
		close(killChan)
		close(outputChan)
	}()

	t.Log(input)
	for output := range outputChan {
		s.True(pass)
		var record define.ETLRecord
		s.NoError(output.To(&record))

		if !cmp.Equal(dimensions, record.Dimensions) {
			diff := cmp.Diff(dimensions, record.Dimensions)
			s.FailNow("dimensions differ: %#v", diff)
		}
		if !cmp.Equal(metrics, record.Metrics) {
			diff := cmp.Diff(metrics, record.Metrics)
			s.FailNow("metrics differ: %s", diff)
		}
	}
	wg.Wait()
}

// TestUsage :
func (s *EventProcessSuite) TestUsage() {
	cases := []struct {
		input      string
		pass       bool
		dimensions map[string]interface{}
		metrics    map[string]interface{}
	}{
		{`{}`, false, nil, nil},
		// 测试正常的输入内容
		{
			`{
				"data_id": 10000.0,
				"version": "v2",
				"event_name": "corefile",
				"event": {
				  "event_content": "corefile found",
				  "bk_count": 123
				},
				"dimension": {
				  "path": "/data/corefile/file.txt"
				},
				"data": {
				  "event_name": "corefile",
				  "event": {
					"event_content": "corefilefound",
					"bk_count": 123
				  },
				  "dimension": {
					"path": "/data/corefile/file.txt"
				  },
				  "timestamp": 1558774691000000.0,
				  "target": "127.0.0.1"
				},
				"target": "127.0.0.1",
				"timestamp": 1558774691000000.0,
				"bk_info": {}
			}`,
			true,
			// dimensions
			map[string]interface{}{
				"dimensions": map[string]interface{}{
					"path": "/data/corefile/file.txt",
				},
				"event_name": "corefile",
				"target":     "127.0.0.1",
			},
			// metrics
			map[string]interface{}{
				"event": map[string]interface{}{
					"event_content": "corefile found",
					"bk_count":      123.0,
				},
			},
		},

		// 缺少event_name
		{
			`{
				"event": {
				  "event_content": "corefile found",
				  "bk_count": 123
				},
				"dimension": {
				  "path": "/data/corefile/file.txt"
				},
				"target": "127.0.0.1",
				"timestamp": 1558774691000000.0
			}`,
			false,
			nil,
			nil,
		},

		// 缺少event
		{
			`{
				"event_name": "corefile",
				"dimension": {
				  "path": "/data/corefile/file.txt"
				},
				"target": "127.0.0.1",
				"timestamp": 1558774691000000.0
			}`,
			false,
			nil,
			nil,
		},

		// 缺少dimension
		{
			`{
				"event_name":"corefile",
				"event":{
					"event_content":"corefile found",
					"bk_count":123
				},
				"target":"127.0.0.1",
				"timestamp":1558774691000000.0
			}`,
			true,
			map[string]interface{}{
				"event_name": "corefile",
				"target":     "127.0.0.1",
				"dimensions": map[string]interface{}{},
			},
			map[string]interface{}{
				"event": map[string]interface{}{
					"event_content": "corefile found",
					"bk_count":      123.0,
				},
			},
		},

		// 缺少timestamp
		{
			`{
				"event_name":"corefile",
				"event":{
					"event_content":"corefile found",
					"bk_count":123
				},
				"dimension":{
					"path":"/data/corefile/file.txt"
				},
				"target":"127.0.0.1"
			}`,
			false,
			nil,
			nil,
		},

		// 缺少target
		{
			`{
				"event_name":"corefile",
				"event":{
					"event_content":"corefile found",
					"bk_count":123
				},
				"dimension":{
					"path":"/data/corefile/file.txt"
				},
				"timestamp":1558774691000000.0
			}`,
			false,
			nil,
			nil,
		},
	}
	for _, c := range cases {
		s.runCase(c.input, c.pass, c.dimensions, c.metrics)
	}
}

// TestProcessSuite :
func TestEventProcessSuite(t *testing.T) {
	suite.Run(t, new(EventProcessSuite))
}
