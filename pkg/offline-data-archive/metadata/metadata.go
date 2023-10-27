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

type Metadata interface {
	PublishShard(ctx context.Context, channelValue interface{}) error
	SubscribeShard(ctx context.Context) <-chan *redis.Message
	GetShardID(ctx context.Context, sd *shard.Shard) (string, error)
	GetAllShards(ctx context.Context) map[string]*shard.Shard
	SetShard(ctx context.Context, k string, sd *shard.Shard) error
	GetShard(ctx context.Context, k string) (*shard.Shard, error)
	GetDistributedLock(ctx context.Context, key, val string, expiration time.Duration) (string, error)
	RenewalLock(ctx context.Context, key string, renewalDuration time.Duration) (bool, error)
	GetPolicies(ctx context.Context, clusterName, tagRouter string) (map[string]*Policy, error)
	GetShards(
		ctx context.Context, clusterName, tagRouter, database string,
	) (map[string]*shard.Shard, error)
	GetReadShardsByTimeRange(
		ctx context.Context, clusterName, tagRouter, database, retentionPolicy string,
		start int64, end int64,
	) ([]*shard.Shard, error)
}

var _ Metadata = (*metadata)(nil)

type metadata struct {
	log log.Logger

	client redis.UniversalClient

	serviceName string
}

func NewMetadata(client redis.UniversalClient, serviceName string, log log.Logger) Metadata {
	return &metadata{
		client:      client,
		serviceName: serviceName,
		log:         log,
	}
}

func (m *metadata) key(k ...string) string {
	return fmt.Sprintf("%s:%s", m.serviceName, strings.Join(k, ":"))
}

func (m *metadata) PublishShard(ctx context.Context, channelValue interface{}) error {
	channelKey := m.key(ShardChannel)
	err := m.client.Publish(ctx, channelKey, channelValue).Err()
	return err
}

func (m *metadata) SubscribeShard(ctx context.Context) <-chan *redis.Message {
	channelKey := m.key(ShardChannel)
	return m.client.Subscribe(ctx, channelKey).Channel()
}

func (m *metadata) GetShardID(ctx context.Context, sd *shard.Shard) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(sd.Unique())), nil
}

func (m *metadata) GetAllShards(ctx context.Context) map[string]*shard.Shard {
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

func (m *metadata) SetShard(ctx context.Context, k string, sd *shard.Shard) error {
	key := m.key(ShardKey, sd.Meta.ClusterName, sd.Meta.TagRouter, sd.Meta.Database)
	res, err := json.Marshal(sd)
	if err != nil {
		m.log.Errorf(ctx, "shard marshal error, shard: %s, err: %s", sd.Unique(), err)
		return err
	}
	return m.client.HSet(ctx, key, k, string(res)).Err()
}

func (m *metadata) GetShard(ctx context.Context, k string) (*shard.Shard, error) {
	splitK := strings.Split(k, "|")
	if len(splitK) < 3 {
		return nil, fmt.Errorf("key format error: %s", k)
	}

	// 从 key 中拆出 clusterName，tagRouter，database 等值
	clusterName, tagRouter, database := splitK[0], splitK[1], splitK[2]
	key := m.key(ShardKey, clusterName, tagRouter, database)
	res, err := m.client.HGet(ctx, key, k).Result()

	if err != nil {
		m.log.Errorf(ctx, "get shard error, key: %s, clusterName: %s, tagRouter: %s, err:%s",
			k, clusterName, tagRouter, err)
		return nil, err
	}

	sd := &shard.Shard{
		Ctx: ctx,
		Log: m.log,
	}

	err = json.Unmarshal([]byte(res), sd)
	if err != nil {
		m.log.Errorf(ctx, "Unmarshal shard error, key: %s, clusterName: %s, tagRouter: %s, err:%s",
			k, clusterName, tagRouter, err)
		return nil, err
	}
	return sd, nil

}

func (m *metadata) GetDistributedLock(ctx context.Context, key, val string, expiration time.Duration) (string, error) {
	var distributedLock string
	_, err := m.client.SetNX(ctx, key, val, expiration).Result()
	if err == nil {
		distributedLock, err = m.client.Get(ctx, key).Result()
	}
	return distributedLock, err
}

// RenewalLock 锁续期
func (m *metadata) RenewalLock(ctx context.Context, key string, renewalDuration time.Duration) (bool, error) {

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

func (m *metadata) GetPolicies(ctx context.Context, clusterName, tagRouter string) (map[string]*Policy, error) {
	key := m.key(PolicyKey, clusterName, tagRouter)
	res, err := m.client.HGetAll(ctx, key).Result()
	if err != nil {
		m.log.Errorf(ctx, "redis HGgetAll %s error: %s", key, err.Error())
		return nil, err
	}

	// policies 的 key 为 存储 policies hashmap 的field，例: default:test_api:bk_biz_id:7
	// 拼接的格式为 clusterName:database:tagRouter
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

func (m *metadata) GetShards(
	ctx context.Context, clusterName, tagRouter, database string,
) (map[string]*shard.Shard, error) {

	key := m.key(ShardKey, clusterName, tagRouter, database)
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

func (m *metadata) GetReadShardsByTimeRange(
	ctx context.Context, clusterName, tagRouter, database, retentionPolicy string,
	start int64, end int64,
) ([]*shard.Shard, error) {
	var (
		shards = make([]*shard.Shard, 0)
		now    = time.Now()
	)

	if retentionPolicy == "" {
		retentionPolicy = "autogen"
	}

	key := m.key(ShardKey, clusterName, tagRouter, database)
	res, err := m.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	errorMessage := make([]string, 0)
	if len(res) == 0 {
		errorMessage = append(errorMessage, fmt.Sprintf("hash key (%s) 's shard is empty", key))
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

			// 通过时间过滤
			querRangeMatch := false

			if sd.Spec.Start.UnixNano() <= start && start < sd.Spec.End.UnixNano() {
				m.log.Infof(ctx, "shard %s is add to query for start: %s <= %d < %s", sd.Unique(), sd.Spec.Start.String(), start, sd.Spec.End.String())
				querRangeMatch = true
			} else {
				if sd.Spec.Start.UnixNano() <= end && end < sd.Spec.End.UnixNano() {
					m.log.Infof(ctx, "shard %s is add to query for end: %s <= %d < %s", sd.Unique(), sd.Spec.Start.String(), end, sd.Spec.End.String())
					querRangeMatch = true
				}
			}

			// 只有时间符合的情况下才判断，避免判断过多
			if querRangeMatch {
				// 验证 shard 状态是否是完成的
				if sd.Status.Code != shard.Finish {
					errorMessage = append(errorMessage, fmt.Sprintf("%s code %d != %d", sd.Unique(), sd.Status.Code, shard.Finish))
					continue
				}

				// 验证 meta 字段
				if sd.Meta.ClusterName != clusterName {
					errorMessage = append(errorMessage, fmt.Sprintf("%s cluster-name %s != %s", sd.Unique(), sd.Meta.ClusterName, clusterName))
					continue
				}
				if sd.Meta.Database != database {
					errorMessage = append(errorMessage, fmt.Sprintf("%s database %s != %s", sd.Unique(), sd.Meta.Database, database))
					continue
				}
				if sd.Meta.TagRouter != tagRouter {
					errorMessage = append(errorMessage, fmt.Sprintf("%s tag-router %s != %s", sd.Unique(), sd.Meta.TagRouter, tagRouter))
					continue
				}
				if sd.Meta.RetentionPolicy != retentionPolicy {
					errorMessage = append(errorMessage, fmt.Sprintf("%s retention-pilicy %s != %s", sd.Unique(), sd.Meta.RetentionPolicy, retentionPolicy))
					continue
				}

				// 判断是否是过期的 shard，只有过期的 shard 才进行查询
				if sd.Spec.Expired.Unix() > now.Unix() {
					errorMessage = append(errorMessage, fmt.Sprintf("shard (%s) 's expired（%s） > now（%s）", sd.Unique(), sd.Spec.Expired.String(), now.String()))
					m.log.Debugf(ctx, "shard %s expired %s is > now %s", sd.Unique(), sd.Spec.Expired.String(), now.String())
					continue
				} else {
					shards = append(shards, sd)
				}
			}
		}
	} else {
		err = fmt.Errorf("start time %d must less than end time %d", start, end)
		return nil, err
	}

	if len(shards) == 0 {
		err = fmt.Errorf(
			"shard nums is 0 reason: %s", strings.Join(errorMessage, " | "),
		)
		return nil, err
	}

	return shards, nil
}
