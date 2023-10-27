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
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// TimeInjectorSuite
type TimeInjectorSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *TimeInjectorSuite) TestUsage() {
	record := define.ETLRecord{
		Time: new(int64),
	}
	*record.Time = time.Now().Add(time.Hour).Unix()

	processor, err := etl.NewTimeInjector(s.CTX, "")
	s.NoError(err)

	var payload define.Payload = define.NewJSONPayload(0)
	s.NoError(payload.From(record))
	s.CheckKillChan(s.KillCh)

	outputChan := make(chan define.Payload, 1)
	processor.Process(payload, outputChan, s.KillCh)

	result := new(define.ETLRecord)
	payload = <-outputChan
	s.NoError(payload.To(result))

	s.True(*result.Time <= time.Now().Unix())
}

// TestTimeInjector
func TestTimeInjector(t *testing.T) {
	suite.Run(t, new(TimeInjectorSuite))
}
