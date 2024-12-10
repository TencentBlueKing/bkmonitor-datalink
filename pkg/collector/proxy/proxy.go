// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/httpmiddleware"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/consul"
)

const routeV2Push = "/v2/push/"

var globalRecords = define.NewRecordQueue(define.PushModeGuarantee)

// Records 返回 Proxy 全局消息管道
func Records() <-chan *define.Record {
	return globalRecords.Get()
}

type Proxy struct {
	pipeline.Validator
	httpSrv        *http.Server
	config         *Config
	consulInstance *consul.Instance
	done           chan struct{}
}

func New(conf *confengine.Config) (*Proxy, error) {
	proxy, err := newProxy(conf)
	if err != nil {
		return nil, err
	}

	router := mux.NewRouter()
	router.HandleFunc(routeV2Push, proxy.V2PushRoute)
	proxy.httpSrv = &http.Server{
		Addr:         proxy.config.Http.Address(),
		Handler:      router,
		ReadTimeout:  time.Minute * 5, // 读超时
		WriteTimeout: time.Minute * 5, // 写超时
	}
	proxy.done = make(chan struct{}, 1)

	for _, mid := range proxy.config.Http.Middlewares {
		fn := httpmiddleware.Get(mid)
		if fn != nil {
			logger.Debugf("proxy use '%s' middleware", mid)
			proxy.httpSrv.Handler = fn(proxy.httpSrv.Handler)
		}
	}

	return proxy, nil
}

func newProxy(conf *confengine.Config) (*Proxy, error) {
	config, err := LoadConfig(conf)
	if err != nil {
		return nil, err
	}

	proxy := &Proxy{config: config}
	return proxy, nil
}

func (p *Proxy) startHttpServer() error {
	if p.config.Disabled {
		logger.Info("proxy: disable http server")
		return nil
	}

	logger.Infof("proxy server listening on %s", p.config.Http.Address())
	return p.httpSrv.ListenAndServe()
}

func (p *Proxy) startConsulHeartbeat() error {
	consulCfg := p.config.Consul.Get()
	if !consulCfg.Enabled {
		logger.Info("proxy: disable consul heartbeat")
		return nil
	}

	var err error
	logger.Infof("proxy consul config: %+v", consulCfg)
	opts := consul.InstanceOptions{
		SrvName:    consulCfg.SrvName,
		Addr:       p.config.Http.Address(),
		Port:       p.config.Http.Port,
		ConsulAddr: consulCfg.Addr,
		Tags:       []string{consulCfg.SrvTag},
		TTL:        consulCfg.TTL,
	}
	logger.Infof("consul instance options: %+v", opts)

	p.consulInstance, err = consul.NewConsulInstance(context.Background(), opts)
	if err != nil {
		return err
	}

	return p.consulInstance.KeepServiceAlive()
}

func (p *Proxy) Start() error {
	// proxy 启动失败不成为关键路径
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		doListen := func() {
			if err := p.startHttpServer(); err != nil {
				if errors.Is(err, http.ErrServerClosed) {
					logger.Info("proxy http server stopped")
					return
				}
				logger.Errorf("failed to start http server: %v", err)
			}
		}

		doListen()
		if !p.config.Http.RetryListen {
			return
		}

		count := 0
		for {
			select {
			case <-p.done:
				return
			case <-ticker.C:
				count++
				logger.Debugf("proxy try listen addr=%v, count=%d", p.config.Http.Address(), count)
				doListen()
			}
		}
	}()

	return p.startConsulHeartbeat()
}

func (p *Proxy) Stop() error {
	close(p.done)

	var err error

	// 关闭 consul 心跳上报
	if p.consulInstance != nil {
		if err = p.consulInstance.CancelService(); err != nil {
			err = errors.Wrap(err, "proxy: cancel consul service error")
		}
	}

	// 优雅关闭 http 服务
	if !p.config.Disabled {
		ctx, cancel := context.WithTimeout(context.Background(), define.ShutdownTimeout)
		defer cancel()
		if err = p.httpSrv.Shutdown(ctx); err != nil {
			err = errors.Wrap(err, "proxy: close http server error")
		}
	}

	return err
}
