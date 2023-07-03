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
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Instance 标识一个 Consul 连接实例
type Instance struct {
	ctx         context.Context
	cancel      context.CancelFunc
	wg          *sync.WaitGroup
	client      *Client
	serviceID   string
	serviceName string
	checkID     string
	tags        []string
	address     string
	port        int
	ttl         string
}

type InstanceOptions struct {
	SrvName    string
	Addr       string
	Port       int
	ConsulAddr string
	Tags       []string
	TTL        string
}

func NewConsulInstance(ctx context.Context, opt InstanceOptions) (*Instance, error) {
	checkID := fmt.Sprintf("%s:%s:%d", strings.Join(opt.Tags, "-"), opt.Addr, opt.Port)

	hash := fnv.New32a()
	_, err := hash.Write([]byte(checkID))
	if err != nil {
		return nil, err
	}

	serviceID := fmt.Sprintf("%s-%d", opt.SrvName, hash.Sum32())
	client, err := NewClient(opt.ConsulAddr)
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
		serviceName: opt.SrvName,
		checkID:     checkID,
		tags:        opt.Tags,
		address:     opt.Addr,
		port:        opt.Port,
		ttl:         opt.TTL,
	}, nil
}

func (i *Instance) Wait() {
	i.wg.Wait()
}

func (i *Instance) GetOrCreateService() error {
	return i.client.GetOrCreateService(i.serviceID, i.serviceName, i.tags, i.address, i.port)
}

func (i *Instance) DeregisterService() error {
	return i.client.ServiceDeregister(i.serviceID)
}

func (i *Instance) CancelService() error {
	i.cancel()
	return nil
}

func (i *Instance) KeepServiceAlive() error {
	duration, err := time.ParseDuration(i.ttl)
	if err != nil {
		return err
	}

	// 1/3 of ttl to awake check
	duration = duration / 3
	if duration <= 0 {
		return errors.New("wrong ttl")
	}

	if err = i.GetOrCreateService(); err != nil {
		return err
	}

	logger.Debugf("consul service id: %s awoken", i.serviceID)
	if err = i.CheckRegister(); err != nil {
		return err
	}

	logger.Debugf("consul check id: %s registered", i.checkID)
	ticker := time.NewTicker(duration)

	go func() {
		defer func() {
			if err := i.CheckDeregister(); err != nil {
				logger.Errorf("deregister check: %s failed,error: %s", i.checkID, err)
			}

			if err := i.CancelService(); err != nil {
				logger.Errorf("cancel service: %s failed, error: %s", i.serviceID, err)
			}
			logger.Debugf("cancel service: %s with check: %s done", i.serviceID, i.checkID)
		}()

		for {
			select {
			case <-i.ctx.Done():
				return
			case <-ticker.C:
				logger.Debugf("consul check id: %s send", i.checkID)
				if err := i.CheckPass(); err != nil {
					logger.Errorf("check pass failed, service: %s, error: %s", i.serviceID, err)
				}
			}
		}
	}()

	return nil
}

func (i *Instance) CheckRegister() error {
	return i.client.CheckRegister(i.serviceID, i.checkID, i.ttl)
}

func (i *Instance) CheckDeregister() error {
	return i.client.CheckDeregister(i.checkID)
}

func (i *Instance) CheckPass() error {
	return i.client.CheckPass(i.checkID, "pass")
}
