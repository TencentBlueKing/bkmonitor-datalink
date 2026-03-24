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
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	consulUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/consul"
)

type Instance struct {
	Ctx       context.Context
	Client    *consulUtils.Instance
	APIClient *api.Client
}

// consul instance
var instance *Instance

func NewInstance(ctx context.Context, opt consulUtils.InstanceOptions) (*Instance, error) {
	var e error
	consulOnce.Do(func() {
		client, err := consulUtils.NewConsulInstance(ctx, opt)
		if err != nil {
			logger.Errorf("new consul instance error, %v", err)
			e = err
			return
		}
		// new a kv client
		conf := api.DefaultConfig()
		conf.Address = opt.Addr
		apiClient, err := api.NewClient(conf)
		if err != nil {
			logger.Errorf("new consul api client error, %v", err)
			e = err
			return
		}
		instance = &Instance{Ctx: ctx, Client: client, APIClient: apiClient}
	})

	return instance, e
}

var consulOnce sync.Once

// GetInstance get a consul instance
func GetInstance() (*Instance, error) {
	if instance != nil {
		return instance, nil
	}
	opt := consulUtils.InstanceOptions{
		SrvName:    config.StorageConsulSrvName,
		Addr:       config.StorageConsulAddress,
		Port:       config.StorageConsulPort,
		ConsulAddr: config.StorageConsulAddr,
		Tags:       config.StorageConsulTag,
		TTL:        config.StorageConsulTll,
	}
	return NewInstance(context.TODO(), opt)
}

// Open new a instance
func (c *Instance) Open() error {
	return nil
}

// Put put a key-val
func (c *Instance) Put(key, val string, modifyIndex uint64, expiration time.Duration) error {
	kvPair := &api.KVPair{Key: key, Value: store.String2byte(val), ModifyIndex: modifyIndex}
	metrics.ConsulPutCount(key)
	logger.Infof("Put: try to put 2 consul, key: %s, modifyIndex: %d, kvPair: %v", key, modifyIndex, kvPair)
	_, err := c.APIClient.KV().Put(kvPair, nil)
	if err != nil {
		logger.Errorf("Put: put to consul error, %v", err)
		return err
	}
	logger.Infof("Put: put to consul success, key: %s", key)
	return nil
}

// Get val by key
func (c *Instance) Get(key string) (uint64, []byte, error) {
	var err error
	kvPair, _, err := c.APIClient.KV().Get(key, nil)
	if err != nil {
		logger.Errorf("Get: get consul key: %s error, %v", key, err)
		return 0, nil, err
	}
	if kvPair == nil {
		// Key not exist
		logger.Infof("Get: key: %s not exist from consul", key)
		return 0, nil, nil
	}

	return kvPair.ModifyIndex, kvPair.Value, nil
}

// Delete delete a key
func (c *Instance) Delete(key string) error {
	metrics.ConsulDeleteCount(key)
	_, err := c.APIClient.KV().Delete(key, nil)
	if err != nil {
		logger.Errorf("delete consul key: %s error, %v", key, err)
		return err
	}
	return nil
}

func (c *Instance) ListKeysWithPrefix(prefixPath string) ([]string, error) {
	kvPairs, _, err := c.APIClient.KV().List(prefixPath, nil)
	if err != nil {
		return nil, err
	}
	var fullPaths []string
	for _, key := range kvPairs {
		fullPaths = append(fullPaths, key.Key)
	}
	return fullPaths, nil
}

func (c *Instance) Close() error {
	return nil
}

var NotFoundErr = errors.New("path not found")
