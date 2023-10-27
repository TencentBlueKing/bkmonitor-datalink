// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul_test

import (
	"context"
	"fmt"
	"path"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// RecordProcessorSuite :
type RecordProcessorSuite struct {
	ConfigSuite
}

// SetupTest :
func (s *RecordProcessorSuite) SetupTest() {
	s.ConfigSuite.SetupTest()
	s.Config.Set(consul.ConfKeySamplingDataSubPath, "sampling")
}

// TestUsage :
func (s *RecordProcessorSuite) TestUsage() {
	ctrl := gomock.NewController(s.T())
	defer ctrl.Finish()
	sc := NewMockSourceClient(ctrl)
	cases := []byte(`{"time":1548238146,"metrics":{"iowait":308926.67,"stolen":0,"system":253010.59,"usage":57.9161028431083,"user":955479.84,"idle":6388648.68},"dimensions":{"plat_id":"0","hostname":"license-1","device_name":"cpu0","ip":"127.0.0.1","company_id":"0"}}`)
	payload := define.NewJSONPayloadFrom(cases, 0)
	cfg := config.NewPipelineConfig()
	s.CTX = config.PipelineConfigIntoContext(s.CTX, cfg)
	conKey := path.Join(s.Config.GetString(consul.ConfKeySamplingDataSubPath), s.ResultTableConfig.ResultTable, "fields")
	sc.EXPECT().Put(gomock.Eq(conKey), gomock.Any()).Return(nil).AnyTimes()
	stubs := gostub.Stub(&consul.NewConsulClient, func(ctx context.Context) (consul.SourceClient, error) {
		return sc, nil
	})
	defer stubs.Reset()
	processor, err := consul.NewConsulProcessor(s.CTX, "test")
	s.NoError(err)
	outputCh := make(chan define.Payload)
	killCh := make(chan error)
	go func() {
		processor.Process(payload, outputCh, killCh)
		fmt.Println("end processor.Process")
	}()
	// payload equal cases
	p := <-outputCh
	var result define.ETLRecord
	s.NoError(p.To(&result))
	s.Equal(*result.Time, int64(1548238146))
	s.Equal(len(result.Metrics), 6)
	s.Equal(len(result.Dimensions), 5)
}

// TestRecordProcessorSuite :
func TestRecordProcessorSuite(t *testing.T) {
	suite.Run(t, new(RecordProcessorSuite))
}
