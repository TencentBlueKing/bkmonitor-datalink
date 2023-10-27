// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// GroupInjectorSuite
type GroupInjectorSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *GroupInjectorSuite) TestUsage() {
	cases := []struct {
		payload                      string
		records, dimensions, metrics int
	}{
		{
			`{"dimensions": {"a": 1}, "metrics": {"b": 2}}`,
			1, 1, 1,
		},
		{
			`{"dimensions": {"a": 1}, "metrics": {"b": 2}, "group_info": null}`,
			1, 1, 1,
		},
		{
			`{"dimensions": {"a": 1}, "metrics": {"b": 2}, "group_info": []}`,
			0, 1, 1,
		},
		{
			`{"dimensions": {"a": 1}, "metrics": {"b": 2}, "group_info": [null]}`,
			0, 1, 1,
		},
		{
			`{"dimensions": {"a": 1}, "metrics": {"b": 2}, "group_info": [{"c": "d"}]}`,
			1, 2, 1,
		},
		{
			`{"dimensions": {"a": 1}, "metrics": {"b": 2}, "group_info": [{"c": "d"}]}`,
			1, 2, 1,
		},
		{
			`{"dimensions": {"a": 1}, "metrics": {"b": 2}, "group_info": [{"c": "d"}, {"e": "f"}]}`,
			2, 2, 1,
		},
	}

	s.CheckKillChan(s.KillCh)
	for i, c := range cases {
		outputCh := make(chan define.Payload)
		records := 0
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for payload := range outputCh {
				records++
				record := new(define.ETLRecord)
				s.NoError(payload.To(record), i)
				s.Len(record.Dimensions, c.dimensions, i)
				s.Len(record.Metrics, c.metrics, i)
			}
		}()

		processor, err := etl.NewGroupInjector(s.CTX, "")
		s.NoError(err, i)

		var payload define.Payload = define.NewJSONPayload(0)
		s.NoError(payload.From([]byte(c.payload)), i)

		processor.Process(payload, outputCh, s.KillCh)
		close(outputCh)
		wg.Wait()
		s.Equal(c.records, records, i)
	}
}

// TestGroupInjector
func TestGroupInjector(t *testing.T) {
	suite.Run(t, new(GroupInjectorSuite))
}
