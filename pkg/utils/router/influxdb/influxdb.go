// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	goRedis "github.com/go-redis/redis/v8"
)

const (
	ClusterInfoKey     = "cluster_info"
	HostInfoKey        = "host_info"
	TagInfoKey         = "tag_info"
	HostStatusInfoKey  = "host_info:status"
	ProxyKey           = "influxdb_proxy"
	QueryRouterInfoKey = "query_router_info"
)

var AllKey = []string{
	ClusterInfoKey, HostInfoKey, TagInfoKey, HostStatusInfoKey, ProxyKey, QueryRouterInfoKey,
}

type Router interface {
	Close() error
	Subscribe(ctx context.Context) <-chan *goRedis.Message
	GetClusterInfo(ctx context.Context) (ClusterInfo, error)
	GetHostInfo(ctx context.Context) (HostInfo, error)
	GetTagInfo(ctx context.Context) (TagInfo, error)
	GetHostStatusInfo(ctx context.Context) (HostStatusInfo, error)
	GetHostStatus(ctx context.Context, hostName string) (HostStatus, error)
	GetProxyInfo(ctx context.Context) (ProxyInfo, error)
	GetQueryRouterInfo(ctx context.Context) (QueryRouterInfo, error)
	SubHostStatus(ctx context.Context) <-chan *goRedis.Message
	SetHostStatusRead(ctx context.Context, hostName string, readStatus bool) error
}

type router struct {
	client goRedis.UniversalClient
	prefix string
}

var _ Router = (*router)(nil)

// NewRouter create instance with router，about influxdb information
// include: cluster info, host info and tag info for how to select influxdb's instance
func NewRouter(prefix string, client goRedis.UniversalClient) *router {
	return &router{
		prefix: prefix,
		client: client,
	}
}

func (r *router) Close() error {
	err := r.client.Close()
	if err != nil {
		return err
	}
	return nil
}

// key get cache's key
func (r *router) key(keys ...string) string {
	return fmt.Sprintf("%s:%s", r.prefix, strings.Join(keys, ":"))
}

// Subscribe sub all key
func (r *router) Subscribe(ctx context.Context) <-chan *goRedis.Message {
	return r.client.Subscribe(ctx, r.prefix).Channel()
}

// GetClusterInfo get all cluster info with map
func (r *router) GetClusterInfo(ctx context.Context) (ClusterInfo, error) {
	key := r.key(ClusterInfoKey)

	res, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var clusterInfo = make(ClusterInfo, len(res))
	for k, v := range res {
		cls := &Cluster{}
		err = json.Unmarshal([]byte(v), &cls)
		if err != nil {
			return nil, err
		}
		clusterInfo[k] = cls
	}

	return clusterInfo, nil
}

// GetHostInfo get all host info with map
func (r *router) GetHostInfo(ctx context.Context) (HostInfo, error) {
	key := r.key(HostInfoKey)

	res, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var hostInfo = make(HostInfo, len(res))
	for k, v := range res {
		host := &Host{}
		err = json.Unmarshal([]byte(v), &host)
		if err != nil {
			return nil, err
		}
		hostInfo[k] = host
	}

	return hostInfo, nil
}

// GetTagInfo get all Tag info with map
func (r *router) GetTagInfo(ctx context.Context) (TagInfo, error) {
	key := r.key(TagInfoKey)

	res, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var tagInfo = make(TagInfo, len(res))
	for k, v := range res {
		tag := &Tag{}
		err = json.Unmarshal([]byte(v), &tag)
		if err != nil {
			return nil, err
		}
		tagInfo[k] = tag
	}

	return tagInfo, nil
}

func (r *router) GetHostStatusInfo(ctx context.Context) (HostStatusInfo, error) {
	key := r.key(HostStatusInfoKey)

	res, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var hostStatusInfo = make(HostStatusInfo, len(res))
	for k, v := range res {
		hostStatus := &HostStatus{}
		err = json.Unmarshal([]byte(v), &hostStatus)
		if err != nil {
			return nil, err
		}
		hostStatusInfo[k] = hostStatus
	}
	return hostStatusInfo, nil
}

func (r *router) GetHostStatus(ctx context.Context, hostName string) (HostStatus, error) {
	var hostStatus HostStatus
	key := r.key(HostStatusInfoKey)

	check, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return hostStatus, err
	}
	if check == 0 {
		return hostStatus, nil
	}

	res, err := r.client.HGet(ctx, key, hostName).Result()
	if err != nil {
		return hostStatus, err
	}
	err = json.Unmarshal([]byte(res), &hostStatus)
	return hostStatus, err
}

func (r *router) GetProxyInfo(ctx context.Context) (ProxyInfo, error) {
	key := r.key(ProxyKey)

	res, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var proxyInfo = make(ProxyInfo, len(res))
	for k, v := range res {
		proxy := &Proxy{}
		err = json.Unmarshal([]byte(v), &proxy)
		if err != nil {
			return nil, err
		}
		proxyInfo[k] = proxy
	}
	return proxyInfo, nil
}

func (r *router) GetQueryRouterInfo(ctx context.Context) (QueryRouterInfo, error) {
	key := r.key(QueryRouterInfoKey)

	res, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var rInfo = make(QueryRouterInfo, len(res))
	for k, v := range res {
		qr := &QueryRouter{}
		err = json.Unmarshal([]byte(v), &qr)
		if err != nil {
			return nil, err
		}
		rInfo[k] = qr
	}
	return rInfo, nil
}

func (r *router) SubHostStatus(ctx context.Context) <-chan *goRedis.Message {
	key := r.key(HostStatusInfoKey)
	return r.client.Subscribe(ctx, key).Channel()
}

func (r *router) SetHostStatusRead(ctx context.Context, hostName string, readStatus bool) error {
	var (
		data []byte
	)
	key := r.key(HostStatusInfoKey)
	now := time.Now().Unix()

	oldStatus, err := r.GetHostStatus(ctx, hostName)
	if err != nil {
		return err
	}

	if now > oldStatus.LastModifyTime && readStatus != oldStatus.Read {
		hostStatus := &HostStatus{
			Read:           readStatus,
			LastModifyTime: now,
		}
		data, err = json.Marshal(hostStatus)
		if err != nil {
			return err
		}

		// 写入 redis
		err = r.client.HSet(ctx, key, hostName, string(data)).Err()
		if err != nil {
			return err
		}

		// 发布
		err = r.client.Publish(ctx, r.prefix, HostStatusInfoKey).Err()
		if err != nil {
			return err
		}
	}
	return nil
}
