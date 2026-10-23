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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul/base"
)

// Instance owns a Consul KV client.
type Instance struct {
	ctx    context.Context
	cancel context.CancelFunc
	client *base.Client
}

func NewConsulInstance(ctx context.Context, consulAddress, caFile, keyFile, certFile string) (*Instance, error) {
	return NewConsulInstanceWithAuth(ctx, consulAddress, caFile, keyFile, certFile, nil)
}

func NewConsulInstanceWithAuth(ctx context.Context, consulAddress, caFile, keyFile, certFile string, auth *base.AuthConfig) (*Instance, error) {
	client, err := base.NewClientWithAuth(consulAddress, caFile, keyFile, certFile, auth)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Instance{ctx: ctx, cancel: cancel, client: client}, nil
}

// Watch
func (i *Instance) Watch(path string) (<-chan any, error) {
	return i.client.Watch(path, "")
}

// GetDataWithPrefix
func (i *Instance) GetDataWithPrefix(prefix string) (api.KVPairs, error) {
	result, _, err := i.client.KV.List(prefix, (&api.QueryOptions{}).WithContext(i.ctx))
	return result, err
}

// GetDataWithPrefixContext 获取指定前缀的数据，并允许调用方取消正在进行的 Consul 请求。
func (i *Instance) GetDataWithPrefixContext(ctx context.Context, prefix string) (api.KVPairs, error) {
	result, _, err := i.client.KV.List(prefix, (&api.QueryOptions{}).WithContext(ctx))
	return result, err
}

// GetData
func (i *Instance) GetData(path string) (*api.KVPair, error) {
	result, _, err := i.client.KV.Get(path, (&api.QueryOptions{}).WithContext(i.ctx))
	return result, err
}

// GetDataContext 获取指定路径，并允许调用方取消正在进行的 Consul 请求。
func (i *Instance) GetDataContext(ctx context.Context, path string) (*api.KVPair, error) {
	result, _, err := i.client.KV.Get(path, (&api.QueryOptions{}).WithContext(ctx))
	return result, err
}
