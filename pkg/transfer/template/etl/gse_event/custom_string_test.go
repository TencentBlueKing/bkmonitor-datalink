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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/standard"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/suite"
	"sync"
	"testing"
)

// ProcessSuite :
type CustomStringSuite struct {
	StoreSuite
}

func (s *CustomStringSuite) runCase(input string, pass bool, dimensions map[string]interface{}, outputCount int) {
	hostInfo := models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
		CCTopoBaseModelInfo: &models.CCTopoBaseModelInfo{
			BizID: []int{2},
			Topo: []map[string]string{
				{
					define.RecordBkModuleID: "19",
					"Anduin":                "1",
				}, {
					define.RecordBkModuleID: "18",
					"Guldan":                "2",
				},
			},
		},
	}
	s.StoreHost(&hostInfo).AnyTimes()
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

	processor := NewCustomEventProcessor(s.CTX, "test")
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
func (s *CustomStringSuite) TestUsage() {
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
			   "_bizid_" : 0,
			   "_cloudid_" : 0,
			   "_server_" : "127.0.0.1",
			   "_time_" : "2019-03-02 15:29:24",
			   "_utctime_" : "2019-03-02 07:29:24",
			   "_value_" : [ "This service is offline" ]
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

// TestProcessSuite :
func TestCustomStringSuite(t *testing.T) {
	suite.Run(t, new(CustomStringSuite))
}
