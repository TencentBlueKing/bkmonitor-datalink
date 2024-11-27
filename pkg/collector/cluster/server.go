// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cluster

import (
	"context"
	"net"
	"time"

	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/cluster/pb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var globalRecords = define.NewRecordQueue(define.PushModeGuarantee)

// Records 返回 Cluster Server 全局消息管道
func Records() <-chan *define.Record {
	return globalRecords.Get()
}

// Server 提供一个集群内数据转发的服务
type Server struct {
	pb.UnimplementedClusterServer
	address string
	server  *grpc.Server
}

func NewServer(conf *confengine.Config) (*Server, error) {
	var c Config
	if err := conf.UnpackChild(define.ConfigFieldCluster, &c); err != nil {
		return nil, err
	}

	server := grpc.NewServer()
	pb.RegisterClusterServer(server, &Server{})
	return &Server{
		address: c.Address,
		server:  server,
	}, nil
}

func (s *Server) Start() error {
	logger.Infof("cluster server listening on %s", s.address)
	errs := make(chan error, 1)
	go func() {
		listener, err := net.Listen("tcp", s.address)
		if err != nil {
			errs <- err
			return
		}

		if err := s.server.Serve(listener); err != nil {
			errs <- err
		}
	}()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		go func() {
			for err := range errs {
				logger.Errorf("cluster background tasks got err: %v", err)
			}
		}()
		return nil
	case err := <-errs:
		return err
	}
}

func (s *Server) Stop() {
	s.server.Stop()
}

func (s *Server) Forward(ctx context.Context, r *pb.ForwardRequest) (*pb.ForwardReply, error) {
	return Forward(ctx, r)
}

func Forward(ctx context.Context, req *pb.ForwardRequest) (*pb.ForwardReply, error) {
	defer utils.HandleCrash()
	ip := utils.GetGrpcIpFromContext(ctx)
	start := time.Now()

	logger.Debugf("cluster grpc request: service=traces, remoteAddr=%v", ip)
	switch req.GetRecordType() {
	case define.RecordTraces.S():
		traces, err := ptrace.NewProtoUnmarshaler().UnmarshalTraces(req.Body)
		if err != nil {
			DefaultMetricMonitor.IncDroppedCounter()
			return &pb.ForwardReply{Message: "FAILED"}, err
		}

		r := &define.Record{
			RecordType:    define.RecordTracesDerived,
			RequestType:   define.RequestGrpc,
			RequestClient: define.RequestClient{IP: ip},
			Data:          traces,
		}

		code, processorName, err := validatePreCheckProcessors(r)
		if err != nil {
			err = errors.Wrapf(err, "failed to run pre-check processors, code=%d, ip=%s", code, ip)
			logger.Warn(err)
			DefaultMetricMonitor.IncFailedCheckFailedCounter(processorName, r.Token.Original, int(code))
			return &pb.ForwardReply{Message: "FAILED"}, err
		}

		globalRecords.Push(r)
		DefaultMetricMonitor.IncHandledCounter(r.Token.Original)
		DefaultMetricMonitor.ObserveHandledDuration(start, r.Token.Original)
	}

	return &pb.ForwardReply{Message: "SUCCESS"}, nil
}

func validatePreCheckProcessors(r *define.Record) (define.StatusCode, string, error) {
	getter := pipeline.GetDefaultGetter()
	if getter == nil {
		logger.Debug("no pipeline getter found")
		return define.StatusCodeOK, "", nil
	}

	pl := getter.GetPipeline(define.RecordTracesDerived)
	if pl == nil {
		return define.StatusBadRequest, "", errors.Errorf("unknown pipeline type %v", r.RecordType)
	}

	for _, name := range pl.PreCheckProcessors() {
		inst := getter.GetProcessor(name)
		switch inst.Name() {
		case define.ProcessorTokenChecker:
			if _, err := inst.Process(r); err != nil {
				return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, err
			}
		}
	}
	return define.StatusCodeOK, "", nil
}
