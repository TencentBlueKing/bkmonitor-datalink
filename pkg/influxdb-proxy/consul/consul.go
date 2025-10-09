// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"

	"github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul/base"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

const (
	// VersionBasePath 如果该路径数据变化，说明consul发生了一次完整的数据更新
	VersionBasePath = "version"
	LockBasePath    = "lock_path"
)

var (
	moduleName   = "consul"
	consulClient base.ConsulClient
)

// TotalPrefix 所有的路径都有这个前缀，由于是通过viper获得，所以不能声明成const
var TotalPrefix string

// LockPath 锁路径
var LockPath string

// GetConsulClient :
var GetConsulClient = func(address string, tlsConfig *config.TlsConfig) (base.ConsulClient, error) {
	return base.NewBasicClient(address, tlsConfig)
}

// Init 初始化操作，在调用consul包其他函数前应执行该函数
var Init = func(address string, prefix string, tlsConfig *config.TlsConfig) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	var err error

	TotalPrefix = prefix
	if TotalPrefix == "" {
		TotalPrefix = "influxdb_proxy"
	}
	LockPath = TotalPrefix + "/" + LockBasePath
	initRoutePath()
	initHostPath()
	initClusterPath()
	initTagPath()
	consulClient, err = GetConsulClient(address, tlsConfig)
	if err != nil {
		flowLog.Errorf("create consul client failed,error:%s", err)
		return err
	}
	flowLog.Tracef("done")
	return nil
}

// Reload 重启consul，通常是因为处理http的reload
var Reload = func(address string, prefix string, tlsConfig *config.TlsConfig) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	var err error
	err = Release()
	if err != nil {
		return err
	}
	err = Init(address, prefix, tlsConfig)
	if err != nil {
		return err
	}
	flowLog.Tracef("done")
	return nil
}

// Release 释放client连接,关闭监听
var Release = func() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	flowLog.Tracef("done")
	return consulClient.Close()
}

// ServiceRegister  :
var ServiceRegister = func(serviceName string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	err := consulClient.ServiceRegister(serviceName)
	if err != nil {
		flowLog.Errorf("ServiceRegister failed,error:%s", err)
		return err
	}
	return nil
}

// ServiceDeregister  :
var ServiceDeregister = func(serviceName string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	err := consulClient.ServiceDeregister(serviceName)
	if err != nil {
		flowLog.Errorf("ServiceDeregister failed,error:%s", err)
		return err
	}
	return nil
}

// CheckRegister :
var CheckRegister = func(address string, serviceName string, period string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	err := consulClient.CheckRegister(serviceName, address, period)
	if err != nil {
		flowLog.Errorf("CheckRegister failed,error:%s", err)
		return err
	}
	flowLog.Tracef("done")
	return nil
}

// CheckDeregister :
var CheckDeregister = func(address string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	var err error
	err = consulClient.CheckDeregister(address)
	if err != nil {
		flowLog.Errorf("CheckDeregister failed,error:%s", err)
		return err
	}
	flowLog.Tracef("done")
	return nil
}

// CheckPassing :
var CheckPassing = func(address string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	err := consulClient.CheckPass(address, "pass")
	if err != nil {
		flowLog.Errorf("CheckPass failed,error:%s", err)
		return err
	}
	flowLog.Tracef("done")
	return nil
}

// CheckFail :
var CheckFail = func(address string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	err := consulClient.CheckFail(address, "pass")
	if err != nil {
		flowLog.Errorf("CheckFail failed,error:%s", err)
		return err
	}
	flowLog.Tracef("done")
	return nil
}

// Put 直接写入
var Put = func(path string, data []byte) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	flowLog.Tracef("done")
	return consulClient.Put(path, data)
}

// Delete 直接删除
var Delete = func(path string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	flowLog.Tracef("done")
	return consulClient.Delete(path)
}

func getDataByPaths(paths []string) (api.KVPairs, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	data := make(api.KVPairs, 0)
	for _, path := range paths {
		kvPairs, err := consulClient.GetPrefix(path, "/")
		if err != nil {
			flowLog.Errorf("get all consul data failed,error:%s", err)
			return nil, err
		}
		data = append(data, kvPairs...)
	}
	return data, nil
}

// WatchChange 监听指定地址，当触发监听时，查询内容地址并解析为hash传出
var WatchChange = func(ctx context.Context, watchPath string, contentPaths []string) (<-chan string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	watchChan := make(chan string)
	flowLog.Tracef("start watch,path:%s", watchPath)
	kvChan, err := consulClient.Watch(watchPath, "")
	if err != nil {
		return nil, err
	}
	go func() {
		defer func() {
			close(watchChan)
			flowLog.Tracef("versionChan closed")
		}()
		for {
			select {
			case <-ctx.Done():
				{
					flowLog.Tracef("ctx done")
					return
				}
			case <-kvChan:
				{
					// 如果没有提供内容监听参数，则触发就返回
					if len(contentPaths) == 0 {
						watchChan <- "changed"
						break
					}
					flowLog.Debugf("get change signal,start to get consul data from paths:%v", contentPaths)
					kvPairs, err := getDataByPaths(contentPaths)
					if err != nil {
						flowLog.Errorf("get all consul data failed,error:%s", err)
						break
					}
					// 将kvPairs排序
					sortedPairs := sortKVPairs(kvPairs, watchPath)
					// 序列化排序好的数据
					hashData := hashIt(sortedPairs)
					// 将新hash值传出到外部
					watchChan <- hashData
				}
			}
		}
	}()
	flowLog.Tracef("done")
	return watchChan, nil
}

// WatchVersionInfoChange 观察版本变化，如果发生变化，说明consul发生了一次更新(或周期覆盖)
// 验证覆盖的方案:收到信号后立刻获取consul全量数据，进行hash对比，若hash相同则无事发生
// 该函数不管理旧hash,只负责传出新hash，新旧hash校验由http处理，这样做是为了保证更新的原子性,因为实际更新动作发生在http内部
var WatchVersionInfoChange = func(ctx context.Context) (<-chan string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	path := TotalPrefix + "/" + VersionBasePath + "/"

	contentPaths := []string{HostPath + "/", ClusterPath + "/", RoutePath + "/"}
	return WatchChange(ctx, path, contentPaths)
}
