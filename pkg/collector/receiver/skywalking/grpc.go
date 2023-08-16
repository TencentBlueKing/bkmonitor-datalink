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
	"fmt"
	"io"
	"strings"
	"time"

	conventions "go.opentelemetry.io/collector/semconv/v1.8.0"
	"google.golang.org/grpc/metadata"
	conf "skywalking.apache.org/repo/goapi/collect/agent/configuration/v3"
	common "skywalking.apache.org/repo/goapi/collect/common/v3"
	event "skywalking.apache.org/repo/goapi/collect/event/v3"
	agent "skywalking.apache.org/repo/goapi/collect/language/agent/v3"
	profile "skywalking.apache.org/repo/goapi/collect/language/profile/v3"
	management "skywalking.apache.org/repo/goapi/collect/management/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	authKey         = "authentication"
	userAgentKey    = "user-agent"
	agentVersionKey = "agent-version"
)

// getMetaDataFromContext 提取 metadata 信息
func getMetaDataFromContext(ctx context.Context) metadata.MD {
	if meta, ok := metadata.FromIncomingContext(ctx); ok {
		return meta
	}
	return nil
}

// getTokenFromMetadata 从 metadata 中获取 Token
func getTokenFromMetadata(md metadata.MD) (string, error) {
	if md == nil {
		return "", errors.New("no metadata found in request")
	}

	authentication, ok := md[authKey]
	if !ok || len(authentication) <= 0 {
		return "", errors.New("no authentication found in metadata")
	}
	return authentication[0], nil
}

// getAgentLanguageFromMetadata 从 metadata 中获取探针语言类型
func getAgentLanguageFromMetadata(md metadata.MD) string {
	language := "unknown"
	if md == nil {
		return language
	}

	userAgent := md.Get(userAgentKey)
	if len(userAgent) == 0 {
		return language
	}
	if strings.HasPrefix(userAgent[0], "grpc-java") {
		return "java"
	}
	if strings.HasPrefix(userAgent[0], "grpc-python") {
		return "python"
	}
	return language
}

// getAgentVersionFromMetadata 从 metadata 中获取探针版本
func getAgentVersionFromMetadata(md metadata.MD) string {
	version := "unknown"
	if md == nil {
		return version
	}

	agentVersion := md.Get(agentVersionKey)
	if len(agentVersion) == 0 {
		return version
	}
	return agentVersion[0]
}

type TraceSegmentReportService struct {
	receiver.Publisher
	pipeline.Validator
	agent.UnimplementedTraceSegmentReportServiceServer
}

func (s *TraceSegmentReportService) Collect(stream agent.TraceSegmentReportService_CollectServer) error {
	defer utils.HandleCrash()

	ctx := stream.Context()
	ip := utils.GetGrpcIpFromContext(ctx)
	logger.Debugf("grpc request: service=segmentReport, remoteAddr=%v", ip)

	for {
		segmentObject, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return stream.SendAndClose(&common.Commands{})
			}
			return err
		}
		s.consumeTraces(ctx, segmentObject)
	}
}

func (s *TraceSegmentReportService) consumeTraces(ctx context.Context, segment *agent.SegmentObject) {
	ip := utils.GetGrpcIpFromContext(ctx)

	md := getMetaDataFromContext(ctx)
	token, err := getTokenFromMetadata(md)
	if err != nil {
		logger.Warnf("failed to get token from context, ip=%v, error: %s", ip, err)
		metricMonitor.IncDroppedCounter(define.RequestGrpc, define.RecordTraces)
		return
	}

	// 构造 extraAttrs 对 skywalking 转 ot 的数据进行额外内容补充
	extraAttrs := make(map[string]string)
	extraAttrs[conventions.AttributeTelemetrySDKVersion] = getAgentVersionFromMetadata(md)
	extraAttrs[conventions.AttributeTelemetrySDKLanguage] = getAgentLanguageFromMetadata(md)
	extraAttrs[conventions.AttributeTelemetrySDKName] = "SkyWalking"

	traces := EncodeTraces(segment, token, extraAttrs)
	start := time.Now()
	r := &define.Record{
		RequestType:   define.RequestGrpc,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTraces,
		Data:          traces,
	}

	prettyprint.Pretty(define.RecordTraces, traces)
	code, processorName, err := s.Validate(r)
	if err != nil {
		logger.Warnf("run pre-check failed, service=TraceSegmentReport, code=%d, ip=%v, error: %s", code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestGrpc, define.RecordTraces, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestGrpc, define.RecordTraces, 0, start)
}

type JVMMetricReportService struct {
	receiver.Publisher
	pipeline.Validator
	agent.UnimplementedJVMMetricReportServiceServer
}

func (s *JVMMetricReportService) Collect(ctx context.Context, jvmMetrics *agent.JVMMetricCollection) (*common.Commands, error) {
	defer utils.HandleCrash()
	ip := utils.GetGrpcIpFromContext(ctx)

	md := getMetaDataFromContext(ctx)
	token, err := getTokenFromMetadata(md)
	if err != nil {
		logger.Warnf("failed to get token from context, ip=%v, error: %s", ip, err)
		metricMonitor.IncDroppedCounter(define.RequestGrpc, define.RecordMetrics)
		return &common.Commands{}, err
	}

	data := convertJvmMetrics(jvmMetrics, token)
	r := &define.Record{
		RecordType:    define.RecordMetrics,
		RequestType:   define.RequestGrpc,
		RequestClient: define.RequestClient{IP: ip},
		Data:          data,
	}

	code, processorName, err := s.Validate(r)
	if err != nil {
		logger.Warnf("run pre-check failed, service=JVMMetricReport, code=%d, ip=%v, error: %s", code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestGrpc, define.RecordMetrics, processorName, r.Token.Original, code)
		return &common.Commands{}, err
	}

	s.Publish(r)
	return &common.Commands{}, nil
}

type ConfigurationDiscoveryService struct {
	receiver.SkywalkingConfigFetcher
	conf.UnimplementedConfigurationDiscoveryServiceServer
}

func (s *ConfigurationDiscoveryService) FetchConfigurations(ctx context.Context, req *conf.ConfigurationSyncRequest) (*common.Commands, error) {
	defer utils.HandleCrash()
	ip := utils.GetGrpcIpFromContext(ctx)

	md := getMetaDataFromContext(ctx)
	token, err := getTokenFromMetadata(md)
	if err != nil {
		return &common.Commands{}, err
	}

	// SN 长度为 0 的时候直接结束，不继续执行后续代码逻辑
	swConf := s.Fetch(token)
	if len(swConf.Sn) == 0 {
		err = fmt.Errorf("empty SN number, service=%s, ip=%v", req.GetService(), ip)
		logger.Warn(err)
		return &common.Commands{}, err
	}

	// 构建标识
	args := []*common.KeyStringValuePair{
		{Key: "SerialNumber", Value: swConf.Sn},
		{Key: "UUID", Value: swConf.Sn},
	}

	agentLanguage := getAgentLanguageFromMetadata(md)
	if customKV := s.createCustomParam(agentLanguage, swConf); customKV != nil {
		args = append(args, customKV)
	}

	// 构建下发配置
	var cmds []*common.Command
	cmds = append(cmds, &common.Command{
		Command: "ConfigurationDiscoveryCommand",
		Args:    args,
	})

	return &common.Commands{Commands: cmds}, nil
}

// createCustomParam 构造自定义参数下发配置
func (s *ConfigurationDiscoveryService) createCustomParam(language string, swConf receiver.SkywalkingConfig) *common.KeyStringValuePair {
	var values []string
	for _, rule := range swConf.Rules {
		if !rule.Enabled {
			continue
		}

		switch rule.Target {
		case "cookie":
			values = append(values, "Cookie")
		case "query_parameter": // 不做处理
		default:
			values = append(values, rule.Field)
		}
	}

	// 不同语言所对应的 key 也不同
	mapping := map[string]string{
		"java":   "plugin.http.include_http_headers",
		"python": "collect_http_headers",
	}

	if v, ok := mapping[language]; ok {
		return &common.KeyStringValuePair{
			Key:   v,
			Value: strings.Join(values, ","),
		}
	}

	return nil
}

// 以下为 grpc-service 空实现 避免报错

type EventService struct {
	event.UnimplementedEventServiceServer
}

func (s *EventService) Collect(stream event.EventService_CollectServer) error {
	return nil
}

type ManagementService struct {
	management.UnimplementedManagementServiceServer
}

func (s *ManagementService) ReportInstanceProperties(ctx context.Context, req *management.InstanceProperties) (*common.Commands, error) {
	return &common.Commands{}, nil
}

func (s *ManagementService) KeepAlive(_ context.Context, req *management.InstancePingPkg) (*common.Commands, error) {
	return &common.Commands{}, nil
}

type ProfileService struct {
	profile.UnimplementedProfileTaskServer
}

func (s *ProfileService) GetProfileTaskCommands(_ context.Context, req *profile.ProfileTaskCommandQuery) (*common.Commands, error) {
	return &common.Commands{}, nil
}

func (s *ProfileService) CollectSnapshot(stream profile.ProfileTask_CollectSnapshotServer) error {
	return nil
}

type MeterService struct {
	agent.UnimplementedMeterReportServiceServer
}

func (s *MeterService) Collect(stream agent.MeterReportService_CollectServer) error {
	return nil
}

func (s *MeterService) CollectBatch(batch agent.MeterReportService_CollectBatchServer) error {
	return nil
}

type ClrService struct {
	agent.UnimplementedCLRMetricReportServiceServer
}

func (s *ClrService) Collect(ctx context.Context, req *agent.CLRMetricCollection) (*common.Commands, error) {
	return &common.Commands{}, nil
}
