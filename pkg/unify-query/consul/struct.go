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
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/prometheus/common/model"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul/base"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// Instance
type Instance struct {
	ctx         context.Context
	cancel      context.CancelFunc
	wg          *sync.WaitGroup
	client      *base.Client
	serviceID   string
	serviceName string
	checkID     string
	tags        []string
	address     string
	port        int
	ttl         string
	watchPaths  map[string]<-chan any
	pathLock    sync.Mutex
}

// NewConsulInstance
func NewConsulInstance(
	ctx context.Context, serviceName, consulAddress string, tags []string,
	address string, port int, ttl string, caFile, keyFile, certFile string,
) (*Instance, error) {
	hash := fnv.New32a()
	_, err := hash.Write([]byte(fmt.Sprintf("%s:%d", address, port)))
	if err != nil {
		return nil, err
	}
	serviceID := fmt.Sprintf("%s-unify-query-%d", serviceName, hash.Sum32())
	checkID := fmt.Sprintf("%s:%d", address, port)
	client, err := base.NewClient(consulAddress, caFile, keyFile, certFile)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Instance{
		ctx:         ctx,
		cancel:      cancel,
		wg:          new(sync.WaitGroup),
		client:      client,
		serviceID:   serviceID,
		serviceName: serviceName,
		checkID:     checkID,
		tags:        tags,
		address:     address,
		port:        port,
		ttl:         ttl,
		watchPaths:  make(map[string]<-chan any),
	}, err
}

// Wait
func (i *Instance) Wait() {
	i.wg.Wait()
}

// AwakeService 如果服务不存在，则创建
func (i *Instance) AwakeService() error {
	return i.client.ServiceAwake(i.serviceID, i.serviceName, i.tags, i.address, i.port)
}

// CancelService 注销服务
func (i *Instance) CancelService() error {
	return i.client.ServiceDeregister(i.serviceID)
}

// StopAwakeService
func (i *Instance) StopAwakeService() error {
	i.cancel()
	return nil
}

// LoopAwakeService
func (i *Instance) LoopAwakeService() error {
	// 增加 serviceName 作为是否开启服务注册的开关
	if i.serviceName == "" {
		return nil
	}
	var dTmp model.Duration
	var duration time.Duration
	dTmp, err := model.ParseDuration(i.ttl)
	if err != nil {
		return err
	}
	// 1/3 of ttl to awake check
	duration = time.Duration(dTmp) / 3
	if duration <= 0 {
		return ErrWrongTTL
	}
	err = i.AwakeService()
	if err != nil {
		return err
	}
	log.Debugf(context.TODO(), "consul service id:%s awaked", i.serviceID)
	err = i.CheckRegister()
	if err != nil {
		return err
	}
	log.Debugf(context.TODO(), "consul check id:%s registered", i.checkID)
	ticker := time.NewTicker(duration)
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		defer func() {
			err := i.CheckDeregister()
			if err != nil {
				errCode := errno.ErrStorageConnFailed().
					WithComponent("Consul").
					WithOperation("取消注册检查").
					WithContext("检查ID", i.checkID).
					WithError(err)

				log.ErrorWithCodef(context.TODO(), errCode)
			}
			err = i.CancelService()
			if err != nil {
				errCode := errno.ErrStorageConnFailed().
					WithComponent("Consul").
					WithOperation("注销服务").
					WithContexts(map[string]interface{}{
						"服务ID": i.serviceID,
						"错误":   err.Error(),
					})
				log.ErrorWithCodef(context.TODO(), errCode)
			}
			log.Warnf(context.TODO(), "cancel service:%s with check:%s done", i.serviceID, i.checkID)
		}()
		defer ticker.Stop()

		for {
			select {
			case <-i.ctx.Done():
				return
			case <-ticker.C:
				log.Debugf(context.TODO(), "consul check id:%s send", i.checkID)
				if err := i.CheckPass(); err != nil {
					errCode := errno.ErrStorageConnFailed().
						WithComponent("Consul").
						WithOperation("健康检查通过").
						WithContexts(map[string]interface{}{
							"检查ID": i.checkID,
							"错误":   err.Error(),
						})

					log.ErrorWithCodef(context.TODO(), errCode)
				}
			}
		}
	}()
	return nil
}

// CheckRegister
func (i *Instance) CheckRegister() error {
	return i.client.CheckRegister(i.serviceID, i.checkID, i.ttl)
}

// CheckDeregister
func (i *Instance) CheckDeregister() error {
	return i.client.CheckDeregister(i.checkID)
}

// CheckPass
func (i *Instance) CheckPass() error {
	return i.client.CheckPass(i.checkID, "pass")
}

// Watch
func (i *Instance) Watch(path string) (<-chan any, error) {
	return i.client.Watch(path, "")
}

// WatchOnce: 仅监听，触发一次
func (i *Instance) WatchOnce(path, separator string) (<-chan any, error) {
	i.pathLock.Lock()
	defer i.pathLock.Unlock()
	if ch, has := i.watchPaths[path]; has {
		return ch, nil
	}
	return i.client.Watch(path, separator)
}

// GetDataWithPrefix
func (i *Instance) GetDataWithPrefix(prefix string) (api.KVPairs, error) {
	result, _, err := i.client.KV.List(prefix, nil)
	return result, err
}

// GetData
func (i *Instance) GetData(path string) (*api.KVPair, error) {
	result, _, err := i.client.KV.Get(path, nil)
	return result, err
}
