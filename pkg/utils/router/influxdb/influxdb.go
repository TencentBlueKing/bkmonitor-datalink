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
	"errors"
	"fmt"
	"strings"
	"time"

	goRedis "github.com/go-redis/redis/v8"
)

const (
	ClusterInfoKey                    = "cluster_info"
	HostInfoKey                       = "host_info"
	TagInfoKey                        = "tag_info"
	HostStatusInfoKey                 = "host_info:status"
	ProxyKey                          = "influxdb_proxy"
	QueryRouterInfoKey                = "query_router_info"
	SpaceToResultTableKey             = "space_to_result_table"
	DataLabelToResultTableKey         = "data_label_to_result_table"
	ResultTableDetailKey              = "result_table_detail"
	SpaceToResultTableChannelKey      = "space_to_result_table:channel"
	DataLabelToResultTableChannelKey  = "data_label_to_result_table:channel"
	ResultTableDetailChannelKey       = "result_table_detail:channel"
	ResultTableDetailChannelDeleteKey = "result_table_detail:channel:delete"

	BkAppToSpaceKey        = "bk_app_to_space"
	BkAppToSpaceChannelKey = "bk_app_to_space:channel"
	BlackListKey           = "black_list"
)

var (
	AllKey           = []string{ClusterInfoKey, HostInfoKey, TagInfoKey, BlackListKey}
	SpaceAllKey      = []string{SpaceToResultTableKey, DataLabelToResultTableKey, ResultTableDetailKey, BkAppToSpaceKey}
	SpaceChannelKeys = []string{SpaceToResultTableChannelKey, DataLabelToResultTableChannelKey, ResultTableDetailChannelKey, ResultTableDetailChannelDeleteKey, BkAppToSpaceChannelKey}
)

type Router interface {
	Close() error
	Subscribe(ctx context.Context) <-chan *goRedis.Message
	SubscribeChannels(ctx context.Context, channels ...string) <-chan *goRedis.Message
	GetClusterInfo(ctx context.Context) (ClusterInfo, error)
	GetHostInfo(ctx context.Context) (HostInfo, error)
	GetTagInfo(ctx context.Context) (TagInfo, error)
	GetHostStatusInfo(ctx context.Context) (HostStatusInfo, error)
	GetHostStatus(ctx context.Context, hostName string) (HostStatus, error)
	GetProxyInfo(ctx context.Context) (ProxyInfo, error)
	GetQueryRouterInfo(ctx context.Context) (QueryRouterInfo, error)
	SubHostStatus(ctx context.Context) <-chan *goRedis.Message
	SetHostStatusRead(ctx context.Context, hostName string, readStatus bool) error
	GetBkAppSpace(ctx context.Context, bkApp string) (SpaceUIDList, error)
	GetSpace(ctx context.Context, spaceId string) (Space, error)
	GetResultTableDetail(ctx context.Context, tableId string) (*ResultTableDetail, error)
	GetDataLabelToResultTableDetail(ctx context.Context, dataLabel string) (ResultTableList, error)
	IterGenericKeyResult(ctx context.Context, coreKey string, batchSize int64, genericCh chan GenericKV)
	GetBlackListInfo(ctx context.Context) (BlackListInfo, error)
}

type GenericKV struct {
	Key string
	Val GenericValue
	Err error
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
	k := fmt.Sprintf("%s:%s", r.prefix, strings.Join(keys, ":"))
	return k
}

// Subscribe sub all key
func (r *router) Subscribe(ctx context.Context) <-chan *goRedis.Message {
	return r.client.Subscribe(ctx, r.prefix).Channel()
}

func (r *router) SubscribeChannels(ctx context.Context, channels ...string) <-chan *goRedis.Message {
	actualChannels := make([]string, 0)
	for _, c := range channels {
		actualChannels = append(actualChannels, fmt.Sprintf("%s:%s", r.prefix, c))
	}
	return r.client.Subscribe(ctx, actualChannels...).Channel()
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

func (r *router) GetBkAppSpace(ctx context.Context, bkApp string) (SpaceUIDList, error) {
	var value SpaceUIDList
	err := GetGenericHashKeyResult(r, ctx, BkAppToSpaceKey, bkApp, &value)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (r *router) GetSpace(ctx context.Context, spaceId string) (Space, error) {
	value := Space{}
	err := GetGenericHashKeyResult(r, ctx, SpaceToResultTableKey, spaceId, &value)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (r *router) GetResultTableDetail(ctx context.Context, tableId string) (*ResultTableDetail, error) {
	value := &ResultTableDetail{}
	err := GetGenericHashKeyResult(r, ctx, ResultTableDetailKey, tableId, value)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (r *router) GetDataLabelToResultTableDetail(ctx context.Context, dataLabel string) (ResultTableList, error) {
	value := ResultTableList{}
	err := GetGenericHashKeyResult(r, ctx, DataLabelToResultTableKey, dataLabel, &value)
	if err != nil {
		return nil, err
	}
	return value, nil
}

// IterGenericKeyResult 遍历 Redis 获取 KEY 的完整内容
func (r *router) IterGenericKeyResult(ctx context.Context, coreKey string, batchSize int64, genericCh chan GenericKV) {
	key := r.key(coreKey)
	cursor := uint64(0)
	defer close(genericCh)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			cmd := r.client.HScan(ctx, key, cursor, "", batchSize)
			res, nextCursor, err := cmd.Result()
			if err != nil {
				genericCh <- GenericKV{Err: fmt.Errorf("Fail to HScan key(%s), %v ", key, err)}
				return
			}
			for i := 0; i < len(res); i += 2 {
				item, err := NewGenericValue(coreKey)
				if err != nil {
					genericCh <- GenericKV{Err: fmt.Errorf("Fail to new generic value, %v ", err)}
					return
				}
				err = json.Unmarshal([]byte(res[i+1]), item)
				if err != nil {
					genericCh <- GenericKV{
						Err: fmt.Errorf("Fail to unmarshal value, %s, %v, %v ", res[i], res[i+1], item)}
					continue
				}
				item.Fill(res[i])
				genericCh <- GenericKV{Key: res[i], Val: item}
			}
			if nextCursor == 0 {
				return
			}
			cursor = nextCursor
		}
	}
}

// GetGenericHashKeyResult 从 Redis 获取 HashKey 中某一个键值对
func GetGenericHashKeyResult(r *router, ctx context.Context, coreKey string, fieldKey string, value GenericValue) error {
	key := r.key(coreKey)
	res, err := r.client.HGet(ctx, key, fieldKey).Result()
	if err != nil {
		return err
	}

	err = json.Unmarshal([]byte(res), value)
	if err != nil {
		return err
	}
	value.Fill(fieldKey)
	return nil
}

func NewGenericValue(typeKey string) (stoVal GenericValue, err error) {
	switch typeKey {
	case BkAppToSpaceKey:
		stoVal = &SpaceUIDList{}
	case SpaceToResultTableKey:
		stoVal = &Space{}
	case DataLabelToResultTableKey:
		stoVal = &ResultTableList{}
	case ResultTableDetailKey:
		stoVal = &ResultTableDetail{}
	default:
		err = fmt.Errorf("invalid generic type(%s) ", typeKey)
	}
	return
}

func (r *router) GetBlackListInfo(ctx context.Context) (BlackListInfo, error) {
	key := r.key(BlackListKey)
	res, err := r.client.Get(ctx, key).Result()
	// 如果key不存在，返回空结构体
	if errors.Is(err, goRedis.Nil) {
		return BlackListInfo{}, nil
	}
	// 其他错误返回错误信息
	if err != nil {
		return BlackListInfo{}, err
	}
	var blackListInfo BlackListInfo
	//处理json解析错误
	err = json.Unmarshal([]byte(res), &blackListInfo)
	if err != nil {
		return BlackListInfo{}, err
	}
	return blackListInfo, nil
}
