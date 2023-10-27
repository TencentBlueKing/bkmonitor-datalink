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
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	consulUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/consul"
)

const (
	consulAddressPath    = "store.consul.address"
	consulPortPath       = "store.consul.port"
	consulSrvNamePath    = "store.consul.srv_name"
	consulConsulAddrPath = "store.consul.consul_addr"
	consulTagPath        = "store.consul.tag"
	consulTTLPath        = "store.consul.ttl"
	consulTaskPath       = "store.consul.task"
	ConsulBasePath       = "store.consul.basic_path"
)

func init() {
	viper.SetDefault(consulAddressPath, "")
	viper.SetDefault(consulPortPath, 8500)
	viper.SetDefault(consulSrvNamePath, "bmw")
	viper.SetDefault(consulConsulAddrPath, "127.0.0.1:8500")
	viper.SetDefault(consulTagPath, []string{"bmw"})
	viper.SetDefault(consulTTLPath, "")
	viper.SetDefault(consulTaskPath, "")
	viper.SetDefault(ConsulBasePath, "")
}

type Instance struct {
	ctx       context.Context
	Client    *consulUtils.Instance
	APIClient *api.Client
}

// consul instance
var instance *Instance

func NewInstance(ctx context.Context) (*Instance, error) {
	client, err := consulUtils.NewConsulInstance(
		ctx,
		consulUtils.InstanceOptions{
			SrvName:    viper.GetString(consulSrvNamePath),
			Addr:       viper.GetString(consulAddressPath),
			Port:       viper.GetInt(consulPortPath),
			ConsulAddr: viper.GetString(consulConsulAddrPath),
			Tags:       viper.GetStringSlice(consulTagPath),
			TTL:        viper.GetString(consulTTLPath),
		},
	)
	if err != nil {
		logger.Errorf("new consul instance error, %v", err)
		return nil, err
	}
	// new a kv client
	conf := api.DefaultConfig()
	conf.Address = viper.GetString(consulAddressPath)
	apiClient, err := api.NewClient(conf)
	if err != nil {
		logger.Errorf("new consul api client error, %v", err)
		return nil, err
	}

	return &Instance{ctx: ctx, Client: client, APIClient: apiClient}, nil
}

// GetInstance get a consul instance
func GetInstance(ctx context.Context) (*Instance, error) {
	if instance != nil {
		return instance, nil
	}
	newInstance, err := NewInstance(ctx)
	if err != nil {
		return nil, err
	}
	instance = newInstance
	return instance, nil
}

// Open new a instance
func (c *Instance) Open() error {
	return nil
}

// Put put a key-val
func (c *Instance) Put(key, val string, expiration time.Duration) error {
	kvPair := &api.KVPair{Key: key, Value: store.String2byte(val)}
	_, err := c.APIClient.KV().Put(kvPair, nil)
	if err != nil {
		logger.Errorf("put to consul error, %v", err)
		return err
	}
	return nil
}

// Get get val by key
func (c *Instance) Get(key string) ([]byte, error) {
	var err error
	kvPair, _, err := c.APIClient.KV().Get(key, nil)
	if err != nil {
		logger.Errorf("get consul key: %s error, %v", key, err)
		return nil, err
	}
	if kvPair == nil {
		return nil, NotFoundErr
	}
	return kvPair.Value, nil
}

// Delete delete a key
func (c *Instance) Delete(key string) error {
	_, err := c.APIClient.KV().Delete(key, nil)
	if err != nil {
		logger.Errorf("delete consul key: %s error, %v", key, err)
		return err
	}
	return nil
}

func (c *Instance) Close() error {
	return nil
}

var (
	NotFoundErr = errors.New("path not found")
)
