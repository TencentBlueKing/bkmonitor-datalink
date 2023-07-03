// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
)

const (
	PolicyKey = "policy"
	ShardKey  = "shard"

	ShardChannel = "shard_channel"

	ShardIncKey = "shard_inc"

	CurrentMaxShardIdKey = "current_max_shard_id_key"
)

type Metadata struct {
	log log.Logger

	client redis.UniversalClient

	serviceName string
}

func NewMetadata(client redis.UniversalClient, serviceName string, log log.Logger) *Metadata {
	return &Metadata{
		client:      client,
		serviceName: serviceName,
		log:         log,
	}
}

func (m *Metadata) key(k ...string) string {
	return fmt.Sprintf("%s:%s", m.serviceName, strings.Join(k, ":"))
}

func (m *Metadata) PublishShard(ctx context.Context, channelValue interface{}) error {
	channelKey := m.key(ShardChannel)
	err := m.client.Publish(ctx, channelKey, channelValue).Err()
	return err
}

func (m *Metadata) SubscribeShard(ctx context.Context) <-chan *redis.Message {
	channelKey := m.key(ShardChannel)
	return m.client.Subscribe(ctx, channelKey).Channel()
}

func (m *Metadata) GetShardID(ctx context.Context, sd *shard.Shard) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(sd.Unique())), nil
}

func (m *Metadata) GetAllShards(ctx context.Context) map[string]*shard.Shard {
	shards := make(map[string]*shard.Shard, 0)
	pattern := m.key(fmt.Sprintf("%s*", ShardKey))
	keys := m.client.Keys(ctx, pattern)
	for _, key := range keys.Val() {
		// get shard keys
		res, err := m.client.HGetAll(ctx, key).Result()
		if err != nil {
			m.log.Errorf(ctx, "redis HGgetAll %s error: %s", key, err.Error())
			return nil
		}

		for k, r := range res {
			sd := &shard.Shard{
				Ctx: ctx,
				Log: m.log,
			}
			err = json.Unmarshal([]byte(r), sd)
			if err != nil {
				m.log.Errorf(ctx, "%s json unmarshal error: %s", r, err.Error())
				return nil
			}

			shards[k] = sd
		}
	}
	return shards
}

func (m *Metadata) SetShard(ctx context.Context, k string, sd *shard.Shard) error {
	key := m.key(ShardKey, sd.Meta.ClusterName, sd.Meta.TagName, sd.Meta.TagValue, sd.Meta.Database)
	res, err := json.Marshal(sd)
	if err != nil {
		m.log.Errorf(ctx, "shard marshal error, shard: %s, err: %s", sd.Unique(), err)
		return err
	}
	return m.client.HSet(ctx, key, k, string(res)).Err()
}

func (m *Metadata) GetShard(ctx context.Context, k string) (*shard.Shard, error) {
	splitK := strings.Split(k, "|")
	if len(splitK) < 8 {
		return nil, fmt.Errorf("key format error: %s", k)
	}

	// 从 key 中拆出 clusterName，tagName，tagValue，database 等值
	clusterName, tagName, tagValue, database := splitK[0], splitK[1], splitK[2], splitK[3]
	key := m.key(ShardKey, clusterName, tagName, tagValue, database)
	res, err := m.client.HGet(ctx, key, k).Result()

	if err != nil {
		m.log.Errorf(ctx, "get shard error, key: %s, clusterName: %s, tagName: %s, tagValue:%s, err:%s",
			k, clusterName, tagName, tagValue, err)
		return nil, err
	}

	sd := &shard.Shard{
		Ctx: ctx,
		Log: m.log,
	}

	err = json.Unmarshal([]byte(res), sd)
	if err != nil {
		m.log.Errorf(ctx, "Unmarshal shard error, key: %s, clusterName: %s, tagName: %s, tagValue:%s, err:%s",
			k, clusterName, tagName, tagValue, err)
		return nil, err
	}
	return sd, nil

}

func (m *Metadata) GetDistributedLock(ctx context.Context, key, val string, expiration time.Duration) (string, error) {
	var distributedLock string
	_, err := m.client.SetNX(ctx, key, val, expiration).Result()
	if err == nil {
		distributedLock, err = m.client.Get(ctx, key).Result()
	}
	return distributedLock, err
}

// RenewalLock 锁续期
func (m *Metadata) RenewalLock(ctx context.Context, key string, renewalDuration time.Duration) (bool, error) {

	// 获取该锁剩余的过去时间
	ttl, err := m.client.TTL(ctx, key).Result()
	if err != nil {
		m.log.Errorf(ctx, "get ttl failed, key:%s, err:%s", key, err)
		return false, err
	}

	// 比较过期时间，如果锁剩下的时间 小于等于 每次续期的最小单位，则进行续期
	if ttl.Seconds() <= renewalDuration.Seconds() {
		m.log.Debugf(ctx, "start renewal lock, key:%s", key)
		return m.client.Expire(ctx, key, renewalDuration).Result()
	}

	return true, nil

}

func (m *Metadata) GetPolicies(ctx context.Context, clusterName, tagName, tagValue string) (map[string]*Policy, error) {
	key := m.key(PolicyKey, clusterName, tagName, tagValue)
	res, err := m.client.HGetAll(ctx, key).Result()
	if err != nil {
		m.log.Errorf(ctx, "redis HGgetAll %s error: %s", key, err.Error())
		return nil, err
	}

	// policies 的 key 为 存储 policies hashmap 的field，例: default:test_api:bk_biz_id:7
	// 拼接的格式为 clusterName:database:tagName:tagValue
	policies := make(map[string]*Policy, len(res))
	for k, r := range res {
		policy := &Policy{}
		err = json.Unmarshal([]byte(r), policy)
		if err != nil {
			m.log.Errorf(ctx, "%s json unmarshal error: %s", r, err.Error())
			return nil, err
		}

		// policy 状态如果 false，直接跳过
		if !policy.Enable {
			continue
		}

		policies[k] = policy
	}
	return policies, err
}

func (m *Metadata) GetShards(
	ctx context.Context, clusterName, tagName, tagValue, database string,
) (map[string]*shard.Shard, error) {

	key := m.key(ShardKey, clusterName, tagName, tagValue, database)
	res, err := m.client.HGetAll(ctx, key).Result()
	if err != nil {
		m.log.Errorf(ctx, "redis HGgetAll %s error: %s", key, err.Error())
		return nil, err
	}

	shards := make(map[string]*shard.Shard, len(res))
	for k, r := range res {
		sd := &shard.Shard{
			Ctx: ctx,
			Log: m.log,
		}
		err = json.Unmarshal([]byte(r), sd)
		if err != nil {
			m.log.Errorf(ctx, "%s json unmarshal error: %s", r, err.Error())
			return nil, err
		}

		shards[k] = sd
	}
	return shards, nil
}

func (m *Metadata) GetShardsByTimeRange(
	ctx context.Context, clusterName, tagName, tagValue, database, retentionPolicy string,
	start int64, end int64,
) ([]*shard.Shard, error) {
	var (
		shards = make([]*shard.Shard, 0)
	)
	key := m.key(ShardKey, clusterName, tagName, tagValue, database)
	res, err := m.client.HGetAll(ctx, key).Result()
	if err != nil {
		m.log.Errorf(ctx, "redis HGetAll %s error: %s", key, err.Error())
		return nil, err
	}

	if start < end {
		for _, r := range res {
			sd := &shard.Shard{
				Ctx: ctx,
				Log: m.log,
			}
			err = json.Unmarshal([]byte(r), sd)
			if err != nil {
				m.log.Errorf(ctx, "%s json unmarshal error: %s", r, err.Error())
				return nil, err
			}

			// 验证 meta 字段
			if sd.Meta.ClusterName != clusterName {
				continue
			}
			if sd.Meta.Database != database {
				continue
			}
			if sd.Meta.RetentionPolicy != retentionPolicy {
				continue
			}
			if sd.Meta.TagName != tagName {
				continue
			}
			if sd.Meta.TagValue != tagValue {
				continue
			}

			// 通过时间过滤
			if sd.Spec.Start.UnixNano() >= start && end < sd.Spec.End.UnixNano() {
				shards = append(shards, sd)
			}
		}
	}

	return shards, nil
}
