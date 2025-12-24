// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/http/auth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route"
)

var moduleName = "http"

// Service :
type Service struct {
	// http服务句柄
	mux *http.ServeMux
	// 后台服务上下文
	ctx context.Context
	// 通知所有服务停止的func
	stopFunc context.CancelFunc
	wg       sync.WaitGroup
	// 全局重载锁，确保重载时所有服务已暂停
	lock sync.RWMutex
	// 基础认证信息存储
	auth auth.Auth
	// 服务可用时为true
	available bool
	address   string

	// 装饰器注册
	basicAuthDecorator  []Decorator
	configAuthDecorator []Decorator
	queryDecorator      []Decorator
	writeDecorator      []Decorator
	createDBDecorator   []Decorator
}

// ReloadCfg :
var ReloadCfg = func() error {
	return common.Config.ReadInConfig()
}
var errTemplate = `{"results":[{"error":"%s"}]}`

// NewHTTPService :
func NewHTTPService(mux *http.ServeMux) (*Service, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("NewHTTPService called")
	// 初始化一个空白的httpService, 后续逐渐填充
	service := &Service{
		mux: mux,
	}

	// err := service.makeClusters()
	flowLog.Infof("start to init service")
	err := service.InitService()
	if err != nil {
		flowLog.Errorf("failed to make httpservice for->[%s]", err)
		return nil, err
	}

	// emptyDecorator := []Decorator{}
	// 开启服务
	flowLog.Infof("open service")

	mux.HandleFunc("/api/v2/query", service.decorate(service.RawQueryHandler, service.queryDecorator...))
	mux.HandleFunc("/query", service.decorate(service.QueryHandler, service.queryDecorator...))
	mux.HandleFunc("/write", service.decorate(service.WriteHandler, service.writeDecorator...))
	mux.HandleFunc("/create_database", service.decorate(service.CreateDBHandler, service.createDBDecorator...))
	mux.HandleFunc("/reload", service.decorate(service.ReloadHandler, service.configAuthDecorator...))
	mux.HandleFunc("/debug", service.decorate(service.DebugHandler, service.configAuthDecorator...))
	mux.HandleFunc("/switch", service.decorate(service.SwitchHandler, service.configAuthDecorator...))
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/print", service.decorate(service.PrintHandler, service.basicAuthDecorator...))
	flowLog.Infof("init service successful")
	err = ProxyStartRecord(time.Now().Unix())
	if err != nil {
		flowLog.Errorf("failed to recored start time,error:%s", err)
		return nil, err
	}

	return service, nil
}

// Wait :
func (httpService *Service) Wait() {
	httpService.wg.Wait()
}

// Shutdown :
func (httpService *Service) Shutdown() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Infof("start to shutdown")
	httpService.lock.Lock()
	flowLog.Tracef("get lock")
	defer func() {
		httpService.lock.Unlock()
		flowLog.Tracef("release lock")
	}()
	err := httpService.switchAvailable(httpService.address, false)
	if err != nil {
		flowLog.Errorf("switchAvailable failed,error:%s", err)
	}
	httpService.stopFunc()
	flowLog.Infof("shutdown successful")
}

// Reload :
func (httpService *Service) Reload(flowID uint64) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flowID,
	})
	flowLog.Infof("start to reload proxy")

	var err error
	// 关闭服务，准备重启
	err = httpService.switchAvailable(httpService.address, false)
	if err != nil {
		flowLog.Errorf("switchAvailable failed,error:%s", err)
		// 这里不执行service重启语句，因为后面startWatchHealth会执行
		// return err
	}
	// 关闭之前的ctx
	flowLog.Debugf("stop pre ctx")
	httpService.stopFunc()
	// 等待旧的线程退出
	httpService.Wait()
	// -----------------重启所有组件-----------------
	// 重置上下文
	flowLog.Debugf("renew ctx")
	httpService.ctx, httpService.stopFunc = context.WithCancel(context.Background())
	// 重新读取配置文件
	flowLog.Debugf("reload config")
	err = ReloadCfg()
	if err != nil {
		flowLog.Errorf("config file reload failed,error:%s", err)
		return err
	}
	flowLog.Debugf("reload consul")
	// 配置读取结束,重启consul
	address := common.Config.GetString(common.ConfigKeyConsulAddress)
	prefix := common.Config.GetString(common.ConfigKeyConsulPrefix)
	caCertFile := common.Config.GetString(common.ConfigKeyConsulCACertFile)
	certFile := common.Config.GetString(common.ConfigKeyConsulCertFile)
	keyFile := common.Config.GetString(common.ConfigKeyConsulKeyFile)
	skipVerify := common.Config.GetBool(common.ConfigKeyConsulSkipVerify)
	tlsConfig := &config.TlsConfig{
		CAFile:     caCertFile,
		CertFile:   certFile,
		KeyFile:    keyFile,
		SkipVerify: skipVerify,
	}
	aclToken := common.Config.GetString(common.ConfigKeyConsulACLToken)
	err = consul.Reload(address, prefix, tlsConfig, aclToken)
	if err != nil {
		flowLog.Errorf("consul reload failed,error:%s", err)
		return err
	}
	// 重新向consul注册服务(原服务已跟随旧context被注销)
	watchErr := httpService.loopUpdateHealth()
	if watchErr != nil {
		flowLog.Errorf("watch health start failed,error:%s", watchErr)
	}
	// 重新启动各服务
	flowLog.Debugf("reload backendManager")
	err = backend.Reload(httpService.ctx)
	if err != nil {
		flowLog.Errorf("backendManager reload failed,error:%s", err)
		return err
	}
	flowLog.Debugf("reload clusterManager")
	err = cluster.Reload(httpService.ctx)
	if err != nil {
		flowLog.Errorf("clusterManager reload failed,error:%s", err)
		return err
	}
	flowLog.Debugf("reload routeManager")
	err = route.Reload(httpService.ctx)
	if err != nil {
		flowLog.Errorf("routeManager reload failed,error:%s", err)
		return err
	}
	// 重新生成认证信息
	flowLog.Debugf("renew auth")
	httpService.auth, err = auth.NewBasicAuth()
	if err != nil {
		flowLog.Errorf("reinit authentication failed,error:%s", err)
		return err
	}
	// 再次开始服务监听
	flowLog.Debugf("start watch version")
	err = httpService.watchServiceUpdate()
	if err != nil {
		flowLog.Errorf("route reload failed,error:%s", err)
		return err
	}
	flowLog.Infof("reload proxy successful")

	// 注册redis服务
	flowLog.Debugf("start redis register")
	err = redis.ServiceRegister()
	if err != nil {
		flowLog.Errorf("redis reload failed,error:%s", err)
		return err
	}
	flowLog.Infof("reload redis successful")

	return nil
}

func (httpService *Service) writeBackJson(writer http.ResponseWriter, str string, code int, flowLog *logging.Entry) {
	// 返回结果
	writer.WriteHeader(code)
	errResult := map[string]string{
		"error": str,
	}
	result, err := json.Marshal(errResult)
	if err != nil {
		// 记录writefail日志
		flowLog.Infof("write back json failed after handle request,error:%s", err)
		return
	}
	_, err = writer.Write(result)
	if err != nil {
		// 记录writefail日志
		flowLog.Infof("write back json failed after handle request,error:%s", err)
		return
	}
	return
}

func (httpService *Service) writeBack(writer http.ResponseWriter, str string, code int, flowLog *logging.Entry) {
	// 返回结果
	writer.WriteHeader(code)
	_, err := writer.Write([]byte(str))
	if err != nil {
		// 记录writefail日志
		flowLog.Infof("write back failed after handle request,error:%s", err)
	}
}

func (httpService *Service) checkAvailable() bool {
	httpService.lock.RLock()
	defer httpService.lock.RUnlock()
	return httpService.available
}

// 该方法应该配合外部的写锁进行调用
func (httpService *Service) switchAvailable(address string, flag bool) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var err error
	httpService.available = flag

	serviceName := common.Config.GetString(common.ConfigKeyConsulHealthServiceName)
	period := common.Config.GetString(common.ConfigKeyConsulHealthPeriod)

	if serviceName != "" && period != "" {
		if httpService.available {
			err = consul.CheckPassing(address)
			if err != nil {
				flowLog.Errorf("CheckPassing failed error:%s", err)
				return err
			}
		} else {
			err = consul.CheckFail(address)
			if err != nil {
				flowLog.Errorf("CheckPassing failed error:%s", err)
				return err
			}
		}
	}

	return nil
}
