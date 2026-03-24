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
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
	confv3 "skywalking.apache.org/repo/goapi/collect/agent/configuration/v3"
	commonv3 "skywalking.apache.org/repo/goapi/collect/common/v3"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

const (
	token = "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
)

func TestTraceSegmentReportService(t *testing.T) {
	t.Run("Failed PreCheck", func(t *testing.T) {
		md := metadata.Pairs(authKey, token)
		ctx := metadata.NewIncomingContext(context.Background(), md)
		segment := mockGrpcTraceSegment(1)

		svc := TraceSegmentReportService{}
		svc.Validator = pipeline.Validator{
			Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, errors.New("MUST ERROR")
			},
		}
		svc.consumeTraces(ctx, segment)
	})

	t.Run("Nil Metadata", func(t *testing.T) {
		segment := mockGrpcTraceSegment(1)

		svc := TraceSegmentReportService{}
		svc.consumeTraces(context.Background(), segment)
	})
}

func TestJVMMetricReportService(t *testing.T) {
	t.Run("Failed PreCheck", func(t *testing.T) {
		md := metadata.Pairs(authKey, token)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		svc := JVMMetricReportService{}
		svc.Validator = pipeline.Validator{
			Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, errors.New("MUST ERROR")
			},
		}

		data := &agentv3.JVMMetricCollection{Metrics: []*agentv3.JVMMetric{mockJvmMetrics()}}
		cmds, err := svc.Collect(ctx, data)
		assert.Len(t, cmds.GetCommands(), 0)
		assert.Error(t, err)
	})

	t.Run("Nil Metadata", func(t *testing.T) {
		svc := JVMMetricReportService{}
		cmds, err := svc.Collect(context.Background(), nil)
		assert.Len(t, cmds.GetCommands(), 0)
		assert.Error(t, err)
	})
}

func mockContextByMetaDataMap(m map[string]string) context.Context {
	md := metadata.New(m)
	ctx := metadata.NewIncomingContext(context.Background(), md)
	return ctx
}

func TestGetInfoFromMetadata(t *testing.T) {
	m := map[string]string{
		authKey:         token,
		userAgentKey:    "grpc-java",
		agentVersionKey: "testVersion",
	}
	ctx := mockContextByMetaDataMap(m)
	md := getMetaDataFromContext(ctx)
	assert.NotNil(t, md)

	t.Run("TokenWithNilMD", func(t *testing.T) {
		s, err := getTokenFromMetadata(nil)
		assert.NotNil(t, err)
		assert.Equal(t, "", s)
	})

	t.Run("Token", func(t *testing.T) {
		s, err := getTokenFromMetadata(md)
		assert.Nil(t, err)
		assert.Equal(t, token, s)
	})

	t.Run("AgentLanguageWithNilMD", func(t *testing.T) {
		lang := getAgentLanguageFromMetadata(nil)
		assert.Equal(t, "unknown", lang)
	})

	t.Run("AgentLanguage", func(t *testing.T) {
		lang := getAgentLanguageFromMetadata(md)
		assert.Equal(t, "java", lang)
	})

	t.Run("AgentVersionWithNilMD", func(t *testing.T) {
		version := getAgentVersionFromMetadata(nil)
		assert.Equal(t, "unknown", version)
	})

	t.Run("AgentVersion", func(t *testing.T) {
		version := getAgentVersionFromMetadata(md)
		assert.Equal(t, "testVersion", version)
	})
}

func TestFetchConfigurations(t *testing.T) {
	m := map[string]string{
		authKey:         token,
		userAgentKey:    "grpc-java",
		agentVersionKey: "testVersion",
	}
	// 构造数据
	ctx := mockContextByMetaDataMap(m)
	req := &confv3.ConfigurationSyncRequest{
		Service: "TestService",
	}

	configs := receiver.SkywalkingConfig{
		Sn: "TestSnNumber",
		Rules: []receiver.SkywalkingRule{
			{
				Type:    "Http",
				Enabled: true,
				Target:  "header",
				Field:   "Accept",
			},
			{
				Type:    "Http",
				Enabled: true,
				Target:  "cookie",
				Field:   "language",
			},
			{
				Type:    "Http",
				Enabled: false,
				Target:  "header",
				Field:   "Accept",
			},
			{
				Type:    "Http",
				Enabled: true,
				Target:  "query_parameter",
				Field:   "from",
			},
		},
	}

	svc := &ConfigurationDiscoveryService{}
	svc.SkywalkingConfigFetcher = receiver.SkywalkingConfigFetcher{Func: func(s string) receiver.SkywalkingConfig {
		return configs
	}}

	cmds, err := svc.FetchConfigurations(ctx, req)
	assert.Nil(t, err)
	assert.Len(t, cmds.Commands, 1)

	expectedCmd := commonv3.Command{
		Command: "ConfigurationDiscoveryCommand",
		Args: []*commonv3.KeyStringValuePair{
			{Key: "SerialNumber", Value: "TestSnNumber"},
			{Key: "UUID", Value: "TestSnNumber"},
			{Key: "plugin.http.include_http_headers", Value: "Accept,Cookie"},
		},
	}

	index := 0
	for _, cmd := range cmds.Commands {
		assert.Equal(t, expectedCmd.Command, cmd.Command)
		for _, kv := range cmd.Args {
			arg := expectedCmd.Args[index]
			assert.Equal(t, arg.Key, kv.Key)
			assert.Equal(t, arg.Value, kv.Value)
			index++
		}
	}
	assert.Equal(t, 3, index)
}

func TestFetchConfigurationsNilSn(t *testing.T) {
	m := map[string]string{
		authKey:         token,
		userAgentKey:    "grpc-java",
		agentVersionKey: "testVersion",
	}
	// 构造数据
	ctx := mockContextByMetaDataMap(m)
	req := &confv3.ConfigurationSyncRequest{
		Service: "TestService",
	}

	svc := &ConfigurationDiscoveryService{}

	cmds, err := svc.FetchConfigurations(ctx, req)
	assert.Nil(t, err)
	assert.Len(t, cmds.GetCommands(), 0)
}

func TestEmptyImpl(t *testing.T) {
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

	t.Run("ClrService", func(t *testing.T) {
		svc := &ClrService{}
		_, err := svc.Collect(nil, nil)
		assert.NoError(t, err)
	})
}
