// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline_test

import (
	"sync"
	"testing"

	"github.com/cstockton/go-conv"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// BackendWithCutterAdapterSuite
type BackendWithCutterAdapterSuite struct {
	ETLSuite
}

// TestAsFloat64
func (s *BackendWithCutterAdapterSuite) TestAsFloat64() {
	var (
		wg       sync.WaitGroup
		backend  = NewMockBackend(s.Ctrl)
		killChan = make(chan<- error)
	)

	shipper := config.ShipperConfigFromContext(s.CTX)
	cluster := shipper.AsInfluxCluster()
	cluster.StorageConfig = map[string]interface{}{
		"database":        "test",
		"real_table_name": "test",
	}
	cluster.ClusterConfig = map[string]interface{}{
		"domain_name": "haha.com",
		"port":        1000,
		"schema":      nil,
	}

	backendCutter := pipeline.NewBackendWithCutterAdapter(s.CTX, backend)

	cases := []byte(`{"time":1547616420,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true,"errWrite":"cxk","spaceStr":"","nilValue":null}}`)
	record := new(define.ETLRecord)
	resRecord := new(define.ETLRecord)
	payload := define.NewJSONPayloadFrom(cases, 1)
	s.NoError(payload.To(record))
	backend.EXPECT().String().AnyTimes()
	backend.EXPECT().Push(gomock.Any(), gomock.Any()).DoAndReturn(func(p define.Payload, c chan<- error) {
		defer wg.Done()
		s.NoError(p.To(resRecord))

		name := resRecord.Dimensions["metric_name"]
		value, err := etl.TransformNilFloat64(record.Metrics[conv.String(name)])
		s.NoError(err)

		s.Equal(value, resRecord.Metrics["metric_value"], name)
	}).AnyTimes()
	wg.Add(5)
	backendCutter.Push(payload, killChan)
}

// TestAsIS
func (s *BackendWithCutterAdapterSuite) TestAsIS() {
	var (
		wg       sync.WaitGroup
		backend  = NewMockBackend(s.Ctrl)
		killChan = make(chan<- error)
	)

	shipper := config.ShipperConfigFromContext(s.CTX)
	cluster := shipper.AsInfluxCluster()
	cluster.StorageConfig = map[string]interface{}{
		"database":        "test",
		"real_table_name": "test",
	}
	cluster.ClusterConfig = map[string]interface{}{
		"domain_name": "haha.com",
		"port":        1000,
		"schema":      nil,
	}

	conf := config.PipelineConfigFromContext(s.CTX)
	conf.Option[config.PipelineConfigOptAllowDynamicMetricsAsFloat] = false

	backendCutter := pipeline.NewBackendWithCutterAdapter(s.CTX, backend)
	cases := []byte(`{"time":1547616420,"dimensions":{"index":"0"},"metrics":{"int":1,"float":2.3,"string":"4","bool":true,"errWrite":"cxk","spaceStr":"","nilValue":null}}`)
	record := new(define.ETLRecord)
	resRecord := new(define.ETLRecord)
	payload := define.NewJSONPayloadFrom(cases, 1)
	s.NoError(payload.To(record))
	backend.EXPECT().String().AnyTimes()
	backend.EXPECT().Push(gomock.Any(), gomock.Any()).DoAndReturn(func(p define.Payload, c chan<- error) {
		defer wg.Done()
		s.NoError(p.To(resRecord))
		name := resRecord.Dimensions["metric_name"]
		s.Equal(record.Metrics[conv.String(name)], resRecord.Metrics["metric_value"], name)
	}).AnyTimes()
	wg.Add(len(record.Metrics))
	backendCutter.Push(payload, killChan)
}

// TestBackendWithCutterAdapterSuite
func TestBackendWithCutterAdapterSuite(t *testing.T) {
	suite.Run(t, new(BackendWithCutterAdapterSuite))
}
