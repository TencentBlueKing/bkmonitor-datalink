// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package skywalking

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

const (
	key   = "authentication"
	token = "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
)

func TestTracesFailedPreCheck(t *testing.T) {
	md := metadata.Pairs(key, token)
	ctx := metadata.NewIncomingContext(context.Background(), md)
	segment := mockGrpcTraceSegment(1)

	svc := TraceSegmentReportService{}
	svc.Validator = receiver.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, errors.New("MUST ERROR")
		},
	}
	svc.consumeTraces(ctx, segment)
}

func TestEmptyImpl(t *testing.T) {
	t.Run("ConfigurationDiscoveryService", func(t *testing.T) {
		svc := &ConfigurationDiscoveryService{}
		_, err := svc.FetchConfigurations(nil, nil)
		assert.NoError(t, err)
	})

	t.Run("EventService", func(t *testing.T) {
		svc := &EventService{}
		err := svc.Collect(nil)
		assert.NoError(t, err)
	})

	t.Run("ManagementService", func(t *testing.T) {
		svc := &ManagementService{}
		_, err := svc.KeepAlive(nil, nil)
		assert.NoError(t, err)
		_, err = svc.ReportInstanceProperties(nil, nil)
		assert.NoError(t, err)
	})

	t.Run("ProfileService", func(t *testing.T) {
		svc := &ProfileService{}
		err := svc.CollectSnapshot(nil)
		assert.NoError(t, err)
		_, err = svc.GetProfileTaskCommands(nil, nil)
		assert.NoError(t, err)
	})

	t.Run("JVMMetricReportService", func(t *testing.T) {
		svc := &JVMMetricReportService{}
		_, err := svc.Collect(nil, nil)
		assert.NoError(t, err)
	})

	t.Run("MeterService", func(t *testing.T) {
		svc := &MeterService{}
		err := svc.Collect(nil)
		assert.NoError(t, err)
		err = svc.CollectBatch(nil)
		assert.NoError(t, err)
	})

	t.Run("ClrService", func(t *testing.T) {
		svc := &ClrService{}
		_, err := svc.Collect(nil, nil)
		assert.NoError(t, err)
	})
}
