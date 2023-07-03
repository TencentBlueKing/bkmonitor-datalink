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
	"io"
	"time"

	"google.golang.org/grpc/metadata"
	conf "skywalking.apache.org/repo/goapi/collect/agent/configuration/v3"
	common "skywalking.apache.org/repo/goapi/collect/common/v3"
	event "skywalking.apache.org/repo/goapi/collect/event/v3"
	segment "skywalking.apache.org/repo/goapi/collect/language/agent/v3"
	profile "skywalking.apache.org/repo/goapi/collect/language/profile/v3"
	management "skywalking.apache.org/repo/goapi/collect/management/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	authKey = "authentication"
)

func getTokenFromContext(ctx context.Context) (string, error) {
	meta, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", errors.New("no metadata found in request")
	}

	authentication, ok := meta[authKey]
	if !ok || len(authentication) <= 0 {
		return "", errors.New("no authentication found in metadata")
	}
	return authentication[0], nil
}

type TraceSegmentReportService struct {
	receiver.Publisher
	receiver.Validator
	segment.UnimplementedTraceSegmentReportServiceServer
}

func (s *TraceSegmentReportService) Collect(stream segment.TraceSegmentReportService_CollectServer) error {
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

func (s *TraceSegmentReportService) consumeTraces(ctx context.Context, segment *segment.SegmentObject) {
	ip := utils.GetGrpcIpFromContext(ctx)
	token, err := getTokenFromContext(ctx)
	if err != nil {
		logger.Warnf("failed to get token from context, ip=%v, error %s", ip, err)
		metricMonitor.IncDroppedCounter(define.RequestGrpc, define.RecordTraces)
		return
	}

	traces := EncodeTraces(segment, token)
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
		logger.Warnf("failed to run pre-check processors, code=%d, ip=%v, error %s", code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestGrpc, define.RecordTraces, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestGrpc, define.RecordTraces, 0, start)
}

// 以下为 grpc-service 空实现 避免报错

type ConfigurationDiscoveryService struct {
	conf.UnimplementedConfigurationDiscoveryServiceServer
}

func (s *ConfigurationDiscoveryService) FetchConfigurations(tx context.Context, req *conf.ConfigurationSyncRequest) (*common.Commands, error) {
	return &common.Commands{}, nil
}

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

type JVMMetricReportService struct {
	segment.UnimplementedJVMMetricReportServiceServer
}

func (s *JVMMetricReportService) Collect(ctx context.Context, jvm *segment.JVMMetricCollection) (*common.Commands, error) {
	return &common.Commands{}, nil
}

type MeterService struct {
	segment.UnimplementedMeterReportServiceServer
}

func (s *MeterService) Collect(stream segment.MeterReportService_CollectServer) error {
	return nil
}

func (s *MeterService) CollectBatch(batch segment.MeterReportService_CollectBatchServer) error {
	return nil
}

type ClrService struct {
	segment.UnimplementedCLRMetricReportServiceServer
}

func (s *ClrService) Collect(ctx context.Context, req *segment.CLRMetricCollection) (*common.Commands, error) {
	return &common.Commands{}, nil
}
