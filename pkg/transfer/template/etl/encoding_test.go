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
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// EncodingHandlerSuite
type EncodingHandlerSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *EncodingHandlerSuite) TestUsage() {
	s.CheckKillChan(s.KillCh)

	encoding := "gbk"
	value := "这是中文"
	encoder, err := define.NewCharSetEncoder(encoding)
	s.NoError(err)

	data, err := encoder.String(fmt.Sprintf(`{"data":"%s"}`, value))
	s.NoError(err)

	payload := define.NewJSONPayloadFrom([]byte(data), 0)

	processor, err := etl.NewEncodingHandler(s.CTX, "test", encoding, true)
	s.NoError(err)

	outputChan := make(chan define.Payload, 1)
	processor.Process(payload, outputChan, s.KillCh)
	output := <-outputChan
	result := make(map[string]string, 1)
	s.NoError(output.To(&result))
	s.Equal(value, result["data"])
}

// TestEncodingHandlerSuite
func TestEncodingHandlerSuite(t *testing.T) {
	suite.Run(t, new(EncodingHandlerSuite))
}
