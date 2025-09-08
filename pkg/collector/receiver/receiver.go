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
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/TarsCloud/TarsGo/tars"
	tarstransport "github.com/TarsCloud/TarsGo/tars/transport"
	"github.com/TarsCloud/TarsGo/tars/util/tools"
	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
	"github.com/elastic/beats/libbeat/outputs/transport"
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

	config      Config
	adminServer *http.Server // 管理员服务 一般不对外暴露
	recvServer  *http.Server // 接收服务
	recvTls     *transport.TLSConfig
	grpcServer  *grpc.Server
	tarsServer  *tarstransport.TarsServer
}

var (
	globalRecords          = define.NewRecordQueue(define.PushModeGuarantee)
	globalConfig           Config
	globalSkywalkingConfig map[string]SkywalkingConfig
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

type SkywalkingConfigFetcher struct {
	Func func(s string) SkywalkingConfig
}

func (f SkywalkingConfigFetcher) Fetch(s string) SkywalkingConfig {
	if f.Func != nil {
		return f.Func(s)
	}
	return globalSkywalkingConfig[s]
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
	if c.RecvServer.TLS != nil {
		if tlsConfig, err = tlscommon.LoadTLSServerConfig(c.RecvServer.TLS); err != nil {
			return nil, err
		}
		logger.Infof("receiver start httpserver with tls config: %+v", tlsConfig)
	}

	// 全局状态记录
	globalConfig = c
	globalSkywalkingConfig = LoadConfigFrom(conf)

	return &Receiver{
		config:  c,
		recvTls: tlsConfig,
		recvServer: &http.Server{
			Handler:      RecvHttpRouter(),
			ReadTimeout:  time.Minute * 5, // 读超时
			WriteTimeout: time.Minute * 5, // 写超时
		},
		adminServer: &http.Server{
			Handler:      AdminHttpRouter(),
			ReadTimeout:  time.Minute * 5, // 读超时
			WriteTimeout: time.Minute * 5, // 写超时
		},
	}, nil
}

func (r *Receiver) ready() {
	for name, f := range componentsReady {
		f()
		logger.Infof("register '%s' component", name)
	}
}

func (r *Receiver) Reload(conf *confengine.Config) {
	globalSkywalkingConfig = LoadConfigFrom(conf)
}

func (r *Receiver) startRecvHttpServer() error {
	for _, mid := range r.config.RecvServer.Middlewares {
		fn := httpmiddleware.Get(mid)
		if fn != nil {
			logger.Debugf("receiver/recv-http use '%s' middleware", mid)
			r.recvServer.Handler = fn(r.recvServer.Handler)
		}
	}

	endpoint := r.config.RecvServer.Endpoint
	logger.Infof("start to listen http recv server at: %v", endpoint)
	if r.recvTls != nil {
		c := r.recvTls.BuildModuleConfig(endpoint)
		l, err := tls.Listen("tcp", endpoint, c)
		if err != nil {
			return err
		}
		return r.recvServer.Serve(l)
	}

	l, err := net.Listen("tcp", endpoint)
	if err != nil {
		return err
	}

	logger.Infof("register recv http route: %+v", RecvHttpRoutes())
	return r.recvServer.Serve(l)
}

func (r *Receiver) starAdminHttpServer() error {
	for _, mid := range r.config.AdminServer.Middlewares {
		fn := httpmiddleware.Get(mid)
		if fn != nil {
			logger.Debugf("receiver/admin-http use '%s' middleware", mid)
			r.adminServer.Handler = fn(r.adminServer.Handler)
		}
	}

	endpoint := r.config.AdminServer.Endpoint
	logger.Infof("start to listen http admin server at: %v", endpoint)
	l, err := net.Listen("tcp", endpoint)
	if err != nil {
		return err
	}

	logger.Infof("register http admin route: %+v", AdminHttpRoutes())
	return r.adminServer.Serve(l)
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

func (r *Receiver) startTarsServer() error {
	endpoint := r.config.TarsServer.Endpoint
	logger.Infof("start to listen tars server at: %v", endpoint)

	conf := &tarstransport.TarsServerConf{
		Proto:          r.config.TarsServer.Transport,
		Address:        endpoint,
		MaxInvoke:      tars.MaxInvoke,
		AcceptTimeout:  tools.ParseTimeOut(tars.AcceptTimeout),
		ReadTimeout:    tools.ParseTimeOut(tars.ReadTimeout),
		WriteTimeout:   tools.ParseTimeOut(tars.WriteTimeout),
		HandleTimeout:  tools.ParseTimeOut(tars.HandleTimeout),
		IdleTimeout:    tools.ParseTimeOut(tars.IdleTimeout),
		QueueCap:       tars.QueueCap,
		TCPReadBuffer:  tars.TCPReadBuffer,
		TCPWriteBuffer: tars.TCPWriteBuffer,
		TCPNoDelay:     tars.TCPNoDelay,
	}
	s := NewTarsProtocol(serviceMgr.tarsServants, true)
	r.tarsServer = tarstransport.NewTarsServer(s, conf)
	if err := r.tarsServer.Listen(); err != nil {
		return err
	}
	return r.tarsServer.Serve()
}

func (r *Receiver) Start() error {
	logger.Info("receiver start working...")

	r.ready()
	errs := make(chan error, 8)

	// 启动 Recv HTTP 服务
	r.wg.Add(1)
	go func() {
		r.wg.Done()
		if !r.config.RecvServer.Enabled {
			return
		}
		if err := r.startRecvHttpServer(); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				return
			}
			errs <- err
		}
	}()

	// 启动 Admin HTTP 服务
	r.wg.Add(1)
	go func() {
		r.wg.Done()
		if !r.config.AdminServer.Enabled {
			return
		}
		if err := r.starAdminHttpServer(); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				return
			}
			errs <- err
		}
	}()

	// 启动 Recv GRPC 服务
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

	// 启动 Recv Tars 服务
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if !r.config.TarsServer.Enabled {
			return
		}
		if err := r.startTarsServer(); err != nil {
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

func (r *Receiver) shutdownRecvServer() error {
	if !r.config.RecvServer.Enabled {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), define.ShutdownTimeout)
	defer cancel()

	t0 := time.Now()
	err := r.recvServer.Shutdown(ctx)
	if err != nil {
		return err
	}

	logger.Infof("shutdown recv server, take: %s", time.Since(t0))
	return nil
}

func (r *Receiver) shutdownAdminServer() error {
	if !r.config.AdminServer.Enabled {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), define.ShutdownTimeout)
	defer cancel()

	t0 := time.Now()
	err := r.adminServer.Shutdown(ctx)
	if err != nil {
		return err
	}

	logger.Infof("shutdown admin server, take: %s", time.Since(t0))
	return nil
}

func (r *Receiver) shutdownTarsServer() error {
	if !r.config.TarsServer.Enabled {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), define.ShutdownTimeout)
	defer cancel()

	t0 := time.Now()
	err := r.tarsServer.Shutdown(ctx)
	if err != nil {
		return err
	}

	logger.Infof("shutdown tars server, take: %s", time.Since(t0))
	return nil
}

func (r *Receiver) shutdownGrpcServer() {
	if !r.config.GrpcServer.Enabled {
		return
	}

	t0 := time.Now()
	r.grpcServer.GracefulStop()
	logger.Infof("shutdown grpc server, take: %s", time.Since(t0))
}

func (r *Receiver) Stop() error {
	if err := r.shutdownRecvServer(); err != nil {
		return err
	}
	if err := r.shutdownAdminServer(); err != nil {
		return err
	}
	if err := r.shutdownTarsServer(); err != nil {
		return err
	}

	r.shutdownGrpcServer()

	r.wg.Wait()
	return nil
}
