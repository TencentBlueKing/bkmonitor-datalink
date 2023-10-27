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
	etl2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// CmdbInjectorSuite
type CmdbInjectorSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *CmdbInjectorSuite) TestUsage() {
	var wg sync.WaitGroup
	processor, err := etl2.NewCMDBInjector(s.CTX, "")
	s.NoError(err)
	payload := define.NewJSONPayloadFrom([]byte(`{"dimensions": {"a": 1}, "metrics": {"b": 2}, "group_info": [{"c": "d"}, {"e": "f"}], "bk_cmdb_level":[{"bk_biz_id":3,"bk_set_id":11,"bk_module_id":56,"aa":11111"bb":2222}]}`), 0)
	outputCh := make(chan define.Payload)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for value := range outputCh {
			record := new(define.ETLRecord)
			s.NoError(value.To(record))
			s.Equal(record.Dimensions[define.RecordCMDBLevelFieldName], "[{\"bk_biz_id\":3,\"bk_set_id\":11,\"bk_module_id\":56,\"aa\":11111\"bb\":2222}]")
			s.Equal(record.Dimensions[define.RecordBizIDFieldName], "2")
		}
	}()
	processor.Process(payload, outputCh, s.KillCh)

	close(outputCh)
	wg.Wait()
}

// TestGroupInjector
func TestCmdbInjector(t *testing.T) {
	suite.Run(t, new(CmdbInjectorSuite))
}
