// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package receiver

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
	"github.com/elastic/beats/libbeat/outputs/transport"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/grpcmiddleware"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/httpmiddleware"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Receiver struct {
	wg sync.WaitGroup

	httpServer *http.Server
	httpTls    *transport.TLSConfig
	grpcServer *grpc.Server
	config     Config
}

var (
	globalRecords          = define.NewRecordQueue(define.PushModeGuarantee)
	globalConfig           Config
	globalSkywalkingConfig map[string]SubConfig
)

// Records 返回 Receiver 全局消息管道
func Records() <-chan *define.Record {
	return globalRecords.Get()
}

// publishRecord 将数据推送至 Receiver 全局消息管道
func publishRecord(r *define.Record) {
	globalRecords.Push(r)
}

type Publisher struct {
	Func func(r *define.Record)
}

func (p Publisher) Publish(r *define.Record) {
	if p.Func != nil {
		p.Func(r)
		return
	}
	publishRecord(r)
}

// GetComponentConfig 获取组件全局配置项
func GetComponentConfig() ComponentConfig {
	return globalConfig.Components
}

func fetchSkywalkingConfByToken(token string) SwConf {
	v, ok := globalSkywalkingConfig[token]
	if !ok {
		return SwConf{}
	}

	// 解析 skywalking 应用层级下发的配置
	var conf SwConf
	err := mapstructure.Decode(v.SkywalkingConf, &conf)
	if err != nil {
		logger.Warnf("failed to parse skywalking agent config: %v", err)
		return SwConf{}
	}
	return conf
}

type SkywalkingConfigFetcher struct {
	Func func(s string) SwConf
}

func (f SkywalkingConfigFetcher) Fetch(s string) SwConf {
	if f.Func != nil {
		return f.Func(s)
	}
	return fetchSkywalkingConfByToken(s)
}

// New 返回 Receiver 实例
func New(conf *confengine.Config) (*Receiver, error) {
	var c Config
	var err error

	if err = conf.UnpackChild(define.ConfigFieldReceiver, &c); err != nil {
		return nil, err
	}
	logger.Infof("receiver config: %+v", c)

	var tlsConfig *tlscommon.TLSConfig
	if c.HttpServer.TLS != nil {
		if tlsConfig, err = tlscommon.LoadTLSServerConfig(c.HttpServer.TLS); err != nil {
			return nil, err
		}
		logger.Infof("receiver start httpserver with tls config: %+v", tlsConfig)
	}

	// 全局状态记录
	globalConfig = c
	globalSkywalkingConfig = LoadConfigFrom(conf)

	return &Receiver{
		config:  c,
		httpTls: tlsConfig,
		httpServer: &http.Server{
			Handler:      HttpRouter(),
			ReadTimeout:  time.Minute * 5, // 读超时
			WriteTimeout: time.Minute * 5, // 写超时
		},
	}, nil
}

func (r *Receiver) ready() {
	for k, f := range componentsReady {
		switch k {
		case define.SourceJaeger:
			if GetComponentConfig().Jaeger.Enabled {
				f()
			}
		case define.SourceOtlp:
			if GetComponentConfig().Otlp.Enabled {
				f()
			}
		case define.SourcePushGateway:
			if GetComponentConfig().PushGateway.Enabled {
				f()
			}
		case define.SourceRemoteWrite:
			if GetComponentConfig().RemoteWrite.Enabled {
				f()
			}
		case define.SourceZipkin:
			if GetComponentConfig().Zipkin.Enabled {
				f()
			}
		case define.SourceSkywalking:
			if GetComponentConfig().Skywalking.Enabled {
				f()
			}
		}
	}
}

func (r *Receiver) Reload(conf *confengine.Config) {
	globalSkywalkingConfig = LoadConfigFrom(conf)
}

func (r *Receiver) startHttpServer() error {
	for _, mid := range r.config.HttpServer.Middlewares {
		fn := httpmiddleware.Get(mid)
		if fn != nil {
			logger.Debugf("receiver/http use '%s' middleware", mid)
			r.httpServer.Handler = fn(r.httpServer.Handler)
		}
	}

	endpoint := r.config.HttpServer.Endpoint
	logger.Infof("start to listen http server at: %v", endpoint)
	if r.httpTls != nil {
		c := r.httpTls.BuildModuleConfig(endpoint)
		l, err := tls.Listen("tcp", endpoint, c)
		if err != nil {
			return err
		}
		return r.httpServer.Serve(l)
	}

	l, err := net.Listen("tcp", endpoint)
	if err != nil {
		return err
	}

	logger.Infof("register http route: %+v", HttpRoutes())
	return r.httpServer.Serve(l)
}

func (r *Receiver) startGrpcServer() error {
	endpoint := r.config.GrpcServer.Endpoint
	logger.Infof("start to listen grpc server at: %v", endpoint)

	var opts []grpc.ServerOption
	for _, mid := range r.config.GrpcServer.Middlewares {
		opt := grpcmiddleware.Get(mid)
		if opt != nil {
			logger.Debugf("receiver/grpc use '%s' middleware", mid)
			opts = append(opts, opt)
		}
	}

	r.grpcServer = grpc.NewServer(opts...)
	for _, svc := range serviceMgr.grpcServices {
		svc(r.grpcServer)
	}

	l, err := net.Listen(r.config.GrpcServer.Transport, endpoint)
	if err != nil {
		return err
	}
	return r.grpcServer.Serve(l)
}

func (r *Receiver) Start() error {
	logger.Info("receiver start working...")

	r.ready()
	errs := make(chan error, 8)

	r.wg.Add(1)
	go func() {
		r.wg.Done()
		if !r.config.HttpServer.Enabled {
			return
		}
		if err := r.startHttpServer(); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				logger.Info("receiver http server stopped")
				return
			}
			errs <- err
		}
	}()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if !r.config.GrpcServer.Enabled {
			return
		}
		if err := r.startGrpcServer(); err != nil {
			errs <- err
		}
	}()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		go func() {
			for err := range errs {
				logger.Errorf("receiver background tasks got err: %v", err)
			}
		}()
		return nil
	case err := <-errs:
		return err
	}
}

func (r *Receiver) Stop() error {
	if r.config.HttpServer.Enabled {
		if err := r.httpServer.Close(); err != nil {
			return err
		}
	}

	if r.config.GrpcServer.Enabled {
		r.grpcServer.Stop()
	}

	r.wg.Wait()
	return nil
}
