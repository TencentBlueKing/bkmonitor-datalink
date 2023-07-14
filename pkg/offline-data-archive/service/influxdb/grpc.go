// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/influxdata/influxdb/services/meta"
	"github.com/influxdata/influxdb/services/storage"
	"github.com/influxdata/influxdb/tsdb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/influxdb/proto"
)

type RpcService struct {
	ctx    context.Context
	addr   string
	ln     net.Listener
	serv   *grpc.Server
	logger log.Logger
	metric string

	server *Server

	err chan error
}

func NewService(
	ctx context.Context,
	address string, logger log.Logger,
	timeout time.Duration,
	dir string,
	metric string,
	getShard func(ctx context.Context, clusterName, tagRouter, db, rp string, start, end int64) ([]*shard.Shard, error),
) (*RpcService, error) {
	// 如果 metaDir 目录不存在则新建一个
	metaDir := filepath.Join(dir, "meta")
	err := checkDir(metaDir)
	if err != nil {
		return nil, err
	}

	// load meta client
	metaClient := meta.NewClient(&meta.Config{
		Dir:                 metaDir,
		RetentionAutoCreate: true,
		LoggingEnabled:      false,
	})
	metaClient.WithLogger(logger.ZapLogger())
	if err := metaClient.Open(); err != nil {
		return nil, fmt.Errorf("meta client open error: %s", err.Error())
	}

	walDir := filepath.Join(dir, "wal")
	err = checkDir(walDir)
	if err != nil {
		return nil, err
	}

	dataDir := filepath.Join(dir, "data")
	err = checkDir(dataDir)
	if err != nil {
		return nil, err
	}

	// load tsdb store
	store := tsdb.NewStore(dataDir)
	store.WithLogger(logger.ZapLogger())
	store.EngineOptions.Config.WALDir = walDir
	store.EngineOptions.EngineVersion = "tsm1"

	var serv = &RpcService{
		ctx:    ctx,
		addr:   address,
		serv:   grpc.NewServer(grpc.StreamInterceptor(otelgrpc.StreamServerInterceptor())),
		err:    make(chan error),
		logger: logger,
		metric: metric,
		server: &Server{
			timeout: timeout,

			walDir:  walDir,
			dataDir: dataDir,

			ss:  storage.NewStore(store, metaClient),
			log: logger,

			getShard: getShard,
		},
	}
	return serv, nil
}

func (s *RpcService) Close() error {
	s.serv.GracefulStop()
	return nil
}

func (s *RpcService) Open() error {
	s.logger.Infof(s.ctx, "Starting GRPC service")
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("unable to listen on port %s: %v", s.addr, err)
	}
	s.ln = lis
	s.logger.Infof(s.ctx, "Listening on GRPC with %s", s.ln.Addr())

	// Begin listening for requests in a separate goroutine.
	go s.serve()
	return nil
}

// serve serves the handler from the listener.
func (s *RpcService) serve() {
	remoteRead.RegisterQueryTimeSeriesServiceServer(s.serv, s.server)
	reflection.Register(s.serv)

	if err := s.serv.Serve(s.ln); err != nil && !strings.Contains(err.Error(), "closed") {
		s.err <- fmt.Errorf("listener failed: addr=%s, err=%s", s.ln.Addr(), err)
	}
}
