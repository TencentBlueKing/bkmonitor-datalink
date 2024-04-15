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

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/standard"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// ProcessSuite :
type ProcessSuite struct {
	StoreSuite
}

// TestUsage :
func (s *ProcessSuite) TestUsage() {
	cases := []struct {
		name           string
		input          string
		wantPass       bool
		wantDimensions map[string]interface{}
	}{
		{"t0", `{}`, false, nil},
		{
			"t1",
			`{"time": 1551924409, "dimensions": {"a": 1}, "metrics": {"b": 2}, "group_info": [{"tag": "aaa", "tag1": "aaa1"},{"tag": "bbb", "tag1": "bbb1"}]}`,
			true,
			map[string]interface{}{
				"a": 1.0,
			},
		},
		{
			"t2",
			`{"time": 1551924409, "dimension": {"a": 1}, "metric": {"b": 2}}`,
			false, nil,
		},
		{
			"t3",
			`{"time": 1551924409, "dimensions": {"a": 1}, "metric": {"b": 2}}`,
			false, nil,
		},
		{
			"t4",
			`{"time": 1551924409, "dimensions": {"a": 1}, "metrics": null}`,
			false, nil,
		},
		{
			"t5",
			`{"time": 1551924409, "dimensions": null, "metrics": {"b": 2}}`,
			true,
			map[string]interface{}{},
		},
		{
			"t6",
			`{"time": 1551924409, "dimensions": {}, "metrics": {"b": 2}}`,
			true,
			map[string]interface{}{},
		},
		{
			"t7",
			`{"time": 1551924409, "dimensions": {"a": 1}, "metrics": {}}`,
			false, nil,
		},
		{
			"t8",
			`{"time": 1551924409, "dimensions": {}, "metrics": {"b": 2}, "cloudid": 0, "ip": "127.0.0.1"}`,
			true,
			map[string]interface{}{
				"ip":          "127.0.0.1",
				"bk_cloud_id": "0",
			},
		},
		{
			"t9",
			`{"time": 1551924409, "dimensions": {}, "metrics": {"b": 2}, "cloudid": 0, "ip": "127.0.0.1", "bizid": 2}`,
			true,
			map[string]interface{}{
				"ip":             "127.0.0.1",
				"bk_cloud_id":    "0",
				"bk_supplier_id": "2",
			},
		},
		{
			"t10",
			`{"time": 1551924409, "dimensions": {"bk_biz_id": 2}, "metrics": {"b": 2}, "cloudid": 0, "ip": "127.0.0.1"}`,
			true,
			map[string]interface{}{
				"bk_biz_id":   2.0,
				"ip":          "127.0.0.1",
				"bk_cloud_id": "0",
			},
		},
		{
			"t11",
			`{"time": 1551924409, "dimensions": {"bk_biz_id": 2}, "metrics": {"b": 2}, "cloudid": 0, "ip": "127.0.0.1", "bizid": 3}`,
			true,
			map[string]interface{}{
				"bk_biz_id":      2.0,
				"ip":             "127.0.0.1",
				"bk_cloud_id":    "0",
				"bk_supplier_id": "3",
			},
		},
		{
			"v6字段",
			`{"time": 1551924409, "dimensions": {"bk_biz_id": 2}, "metrics": {"b": 2}, "cloudid": 0, "ip": "127.0.0.1", "bizid": 3, "bk_agent_id": "010000525400c48bdc1670385834306k", "bk_biz_id": 2, "bk_host_id": 30145}`,
			true,
			map[string]interface{}{
				"ip":             "127.0.0.1",
				"bk_cloud_id":    "0",
				"bk_supplier_id": "3",
				"bk_agent_id":    "010000525400c48bdc1670385834306k",
				"bk_biz_id":      "2",
				"bk_host_id":     "30145",
			},
		},
	}
	for _, c := range cases {
		s.T().Run(c.name, func(t *testing.T) {
			payload := define.NewJSONPayloadFrom([]byte(c.input), 0)

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

			processor := standard.NewProcessor(s.CTX, "test")
			go func() {
				processor.Process(payload, outputChan, killChan)
				close(killChan)
				close(outputChan)
			}()

			t.Log(c.input)
			cnt := 0
			for output := range outputChan {
				s.True(c.wantPass)
				var record standard.Record
				s.NoError(output.To(&record))
				for key, value := range record.Dimensions {
					cnt++
					s.Equal(c.wantDimensions[key], value)
				}
			}
			if c.wantPass {
				s.Equalf(len(c.wantDimensions), cnt, "dimension length")
			} else {
				s.Equal(0, cnt, "dimensioon not empty")
			}
			wg.Wait()
		})
	}
}

// TestProcessSuite :
func TestProcessSuite(t *testing.T) {
	suite.Run(t, new(ProcessSuite))
}
