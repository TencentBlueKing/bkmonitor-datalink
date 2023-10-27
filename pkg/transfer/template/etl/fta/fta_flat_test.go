// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fta_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/fta"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// FlatFTATest
type FlatFTATest struct {
	testsuite.ETLSuite
}

// TestMultipleEvents
func (s *FlatFTATest) TestMultipleEvents() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{
			"etl_config": "bk_fta_event"
		}`,
	)

	processor, err := fta.NewFlatFTAProcessor(s.CTX, "test")

	s.NoError(err)

	var results []map[string]interface{}
	s.RunN(2, `{"bk_ingest_time": 1558788888000000, "data": [{"event_name":"port_error","event":{"event_content":"eventdescrition"},"dimension":{"module":"module"},"timestamp":1558774691000000,"target":"127.0.0.1"},{"event_name":"corefile","event":{"event_content":"eventdescrition"},"dimension":{"set":"set"},"timestamp":1558774691000000,"target":"127.0.0.1"}]}`,
		processor,
		func(result map[string]interface{}) {
			results = append(results, result)
		},
	)
	s.MapEqual(map[string]interface{}{
		"event_name": "port_error",
		"event": map[string]interface{}{
			"event_content": "eventdescrition",
		},
		"dimension": map[string]interface{}{
			"module": "module",
		},
		"target":         "127.0.0.1",
		"timestamp":      1558774691000000.0,
		"bk_ingest_time": 1558788888000000.0,
	}, results[0])
	s.MapEqual(map[string]interface{}{
		"event_name": "corefile",
		"event": map[string]interface{}{
			"event_content": "eventdescrition",
		},
		"dimension": map[string]interface{}{
			"set": "set",
		},
		"target":         "127.0.0.1",
		"timestamp":      1558774691000000.0,
		"bk_ingest_time": 1558788888000000.0,
	}, results[1])
}

// TestFlatFTATest :
func TestFlatFTATest(t *testing.T) {
	suite.Run(t, new(FlatFTATest))
}
