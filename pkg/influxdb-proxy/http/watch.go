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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/http/auth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route"
)

// 刷新失败时，应该恢复全部服务到旧版本
func (httpService *Service) backupAllService() {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Infof("start to backup service")
	backend.Backup()
	cluster.Backup()
	route.Backup()
}

// 刷新失败时，应该恢复全部服务到旧版本
func (httpService *Service) recoverAllService() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Infof("start to recover service")
	// 全局信息刷新过程中，阻止所有服务访问
	httpService.lock.Lock()
	defer httpService.lock.Unlock()
	err := backend.Recover()
	if err != nil {
		flowLog.Errorf("recover backend failed,error:%s", err)
		return err
	}
	err = cluster.Recover()
	if err != nil {
		flowLog.Errorf("recover cluster failed,error:%s", err)
		return err
	}
	err = route.Recover()
	if err != nil {
		flowLog.Errorf("recover route failed,error:%s", err)
		return err
	}
	// refreshAllService执行成功,表明三个模块的服务正确启动，此时状态位为true
	err = httpService.switchAvailable(httpService.address, true)
	if err != nil {
		flowLog.Errorf("switchAvailable to true failed,error:%s", err)
	}
	return nil
}

func (httpService *Service) refreshAllServiceWithoutLock(flowLog *logging.Entry) error {
	flowLog.Infof("start to refresh all service")
	var err error
	err = httpService.switchAvailable(httpService.address, false)
	if err != nil {
		flowLog.Errorf("switchAvailable to false failed,error:%s", err)
		// 报错就尝试重新建立service health,因为可能是consul重启丢失了服务
		serviceName := common.Config.GetString(common.ConfigKeyConsulHealthServiceName)
		period := common.Config.GetString(common.ConfigKeyConsulHealthPeriod)
		if period != "" && serviceName != "" {
			err = httpService.registerHealth(serviceName, period, flowLog)
			if err != nil {
				flowLog.Errorf("registerHealth failed,error:%s", err)
			}
		} else {
			flowLog.Warnf("get empty service config,reinit health failed")
		}
		// return err
	}
	err = backend.Refresh()
	if err != nil {
		flowLog.Errorf("backendManage Refresh failed,error:%s", err)
		return err
	}
	err = cluster.Refresh()
	if err != nil {
		flowLog.Errorf("clusterManage Refresh failed,error:%s", err)
		return err
	}
	err = route.Refresh()
	if err != nil {
		flowLog.Errorf("route Refresh failed,error:%s", err)
		return err
	}
	// refreshAllService执行成功,表明三个模块的服务正确启动，此时状态位为true
	err = httpService.switchAvailable(httpService.address, true)
	if err != nil {
		flowLog.Errorf("switchAvailable to true failed,error:%s", err)
		// 上面已经重新注册了，这里没必要再注册一次
		// return err
	}
	flowLog.Infof("refresh all service successful")
	return nil
}

func (httpService *Service) refreshAllService() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Infof("called")
	// 全局信息刷新过程中，阻止所有服务访问
	httpService.lock.Lock()
	defer httpService.lock.Unlock()
	return httpService.refreshAllServiceWithoutLock(flowLog)
}

// 监听服务信息更新，包括route，cluster，host
func (httpService *Service) watchServiceUpdate() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var err error
	flowLog.Infof("start to watch all service update")
	infoChan, err := consul.WatchVersionInfoChange(httpService.ctx)
	if err != nil {
		flowLog.Errorf("start watch route from consul failed,error:%s", err)
		return err
	}
	flowLog.Infof("start to catch first data from consul")
	initData := <-infoChan
	err = httpService.refreshAllServiceWithoutLock(flowLog)
	if err != nil {
		flowLog.Errorf("init service data from consul failed,error:%s", err)
		return err
	}
	oldHash := initData
	flowLog.Infof("loop watching consul data")
	httpService.wg.Add(1)
	go func() {
		defer httpService.wg.Done()
		for {
			select {
			case <-httpService.ctx.Done():
				{
					flowLog.Tracef("ctx done,stop watching services")
					return
				}
			case hashData, ok := <-infoChan:
				{
					flowLog.Debugf("get update signal from consul")
					if !ok {
						flowLog.Tracef("version watch channel canceled,watch stop")
						return
					}
					flowLog.Debugf("get new consul hash:%s", hashData)
					if hashData == oldHash {
						flowLog.Tracef("nothing changed,refresh will not start")
						break
					}
					flowLog.Infof("data in consul changed,refresh start")
					// 刷新前进行一次备份，以备刷新恢复
					httpService.backupAllService()
					err = httpService.refreshAllService()
					if err != nil {
						flowLog.Errorf("refresh failed,try to recover service,error:%s", err)
						err = httpService.recoverAllService()
						if err != nil {
							flowLog.Errorf("recover failed,error:%s", err)
							break
						}
						flowLog.Infof("service refresh failed,but recover success,proxy will use old service data")
						break
					}
					// 更新hash值
					flowLog.Infof("refresh success,hash update,new hash:%s", hashData)
					oldHash = hashData

					// // 只有刷新成功的机器可以参与rebalance
					// err := httpService.Rebalance()
					// if err != nil {
					// 	flowLog.Errorf("rebalance tag failed,error:%s", err)
					// 	break
					// }
				}

			}
		}
	}()

	return nil
}

// registerHealth 将proxy服务注册到consul
func (httpService *Service) registerHealth(serviceName string, period string, flowLog *logging.Entry) error {
	var err error
	flowLog.Debugf("start to regist health")
	err = consul.ServiceRegister(serviceName)
	if err != nil {
		flowLog.Errorf("ServiceRegister failed,error:%s", err)
		return err
	}
	err = consul.CheckRegister(httpService.address, serviceName, period)
	if err != nil {
		flowLog.Errorf("CheckRegister failed,error:%s", err)
		return err
	}
	flowLog.Debugf("regist health done")
	return nil
}

// startWarchHealth: 定期更新consul上的proxy状态信息
func (httpService *Service) loopUpdateHealth() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("start to watch health")
	var err error
	serviceName := common.Config.GetString(common.ConfigKeyConsulHealthServiceName)
	period := common.Config.GetString(common.ConfigKeyConsulHealthPeriod)
	d, err := time.ParseDuration(period)
	if err != nil {
		flowLog.Errorf("parse duration failed when startWatchHealth,period:%s,error:%s", period, err)
		return err
	}
	// 发送周期为check周期的1/3
	timer := time.NewTicker(d / 3)
	if period != "" && serviceName != "" {
		// 注册心跳
		err = httpService.registerHealth(serviceName, period, flowLog)
		if err != nil {
			flowLog.Errorf("registerHealth failed,error:%s", err)
			return err
		}
	} else {
		// 如果配置不存在则停止状态检查
		timer.Stop()
		flowLog.Warnf("get empty service config,reinit health failed")
	}
	httpService.wg.Add(1)
	go func() {
		defer httpService.wg.Done()
		flowLog.Tracef("start watching")
		for {
			select {
			case <-httpService.ctx.Done():
				{
					flowLog.Tracef("ctx done,return")
					timer.Stop()
					err = consul.ServiceDeregister(serviceName)
					if err != nil {
						flowLog.Errorf("ServiceDeregister failed,error:%s", err)
					}
					return
				}
			case <-timer.C:
				{
					if httpService.checkAvailable() {
						if serviceName != "" && period != "" {
							flowLog.Tracef("send health signal")
							// refreshAllService执行成功,表明三个模块的服务正确启动，此时状态位为true
							err = consul.CheckPassing(httpService.address)
							if err != nil {
								flowLog.Errorf("send passing signal failed,error:%s", err)
								// 报错就尝试重新建立service health,因为可能是consul重启丢失了服务
								flowLog.Infof("try reinit consul health")
								err = httpService.registerHealth(serviceName, period, flowLog)
								if err != nil {
									flowLog.Errorf("regist health failed,error:%s", err)
								}
								err = consul.CheckPassing(httpService.address)
								if err != nil {
									flowLog.Errorf("send passing signal still failed after reinit consul health,turn consul state to down,error:%s", err)
									err = ConsulAliveDown()
									if err != nil {
										flowLog.Errorf("consul alive down,error:%s", err)
									}
								}
								break
							}
						}
						err = ConsulAliveUp()
						if err != nil {
							flowLog.Errorf("consul alive up,error:%s", err)
						}
					}
				}
			}
		}
	}()
	flowLog.Tracef("done")
	return nil
}

// InitService 进行路由全体信息的初始化及监听
func (httpService *Service) InitService() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Infof("start to init service")
	httpService.lock.Lock()
	defer httpService.lock.Unlock()
	var err error
	// 生成上下文
	httpService.ctx, httpService.stopFunc = context.WithCancel(context.Background())
	// 从viper中获取配置
	listen := common.Config.GetString(common.ConfigHTTPAddress)
	port := common.Config.GetString(common.ConfigHTTPPort)
	httpService.address = listen + ":" + port
	address := common.Config.GetString(common.ConfigKeyConsulAddress)
	prefix := common.Config.GetString(common.ConfigKeyConsulPrefix)
	aclToken := common.Config.GetString(common.ConfigKeyConsulACLToken)
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
	err = consul.Init(address, prefix, tlsConfig, aclToken)
	if err != nil {
		flowLog.Errorf("consul init failed")
		return err
	}
	flowLog.Infof("start watch health...")
	// 周期查询状态，若状态位为true则发送health signal到consul
	err = httpService.loopUpdateHealth()
	if err != nil {
		flowLog.Errorf("watch health start failed,error:%s", err)
		return err
	}

	flowLog.Infof("init backend...")
	// 初始化
	err = backend.Init(httpService.ctx)
	if err != nil {
		flowLog.Errorf("init backend failed,error:%s", err)
		return err
	}

	flowLog.Infof("init cluster...")
	err = cluster.Init(httpService.ctx)
	if err != nil {
		flowLog.Errorf("init cluster failed,error:%s", err)
		return err
	}
	flowLog.Infof("init route...")
	err = route.Init(httpService.ctx)
	if err != nil {
		flowLog.Errorf("init route failed,error:%s", err)
		return err
	}
	flowLog.Infof("init authentication...")
	// 生成认证信息
	httpService.auth, err = auth.NewBasicAuth()
	if err != nil {
		flowLog.Errorf("BasicAuth init failed")
		return err
	}
	flowLog.Infof("init consul watcher...")
	// 启动监听
	err = httpService.watchServiceUpdate()
	if err != nil {
		flowLog.Errorf("start to watch service failed,error:%s", err)
		return err
	}
	// 初始化装饰器列表
	httpService.initDecorator()
	flowLog.Infof("init service successful")
	return nil
}
