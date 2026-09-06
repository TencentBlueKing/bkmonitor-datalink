// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	goredis "github.com/go-redis/redis"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type ClientOfRedis interface {
	LLen(key string) *goredis.IntCmd
	Ping() *goredis.StatusCmd
	Close() error
}

// NewRedisPipeline : redis pipeline
var NewRedisPipeline = func(cli ClientOfRedis) goredis.Pipeliner {
	client := cli.(*goredis.Client)
	return client.Pipeline()
}

// ClientPing : goredis 在创建client 失败时,不会返回错误,故需要先ping 一下
var ClientPing = func(client ClientOfRedis) error {
	return client.Ping().Err()
}

var NewRedisClient = func(dbInfo *config.RedisMetaClusterInfo, auth *config.SimpleMetaAuthInfo) ClientOfRedis {
	passWord, err := auth.GetPassword()
	if err != nil {
		logging.Warnf("redis may not establish connection %v: password", define.ErrGetAuth)
	}

	if dbInfo.GetIsSentinel() {
		return goredis.NewFailoverClient(&goredis.FailoverOptions{
			MasterName:    dbInfo.GetMaster(),
			DB:            dbInfo.GetDB(),
			Password:      passWord,
			SentinelAddrs: []string{fmt.Sprintf("%s:%d", dbInfo.GetDomain(), dbInfo.GetPort())},
		})
	}
	return goredis.NewClient(&goredis.Options{
		DB:       dbInfo.GetDB(),
		Addr:     fmt.Sprintf("%s:%d", dbInfo.GetDomain(), dbInfo.GetPort()),
		Password: passWord,
	})
}

type Backend struct {
	*define.BaseBackend
	*define.ProcessorMonitor
	ctx             context.Context
	cancelFunc      context.CancelFunc
	client          ClientOfRedis
	pipe            goredis.Pipeliner
	key             string
	wg              sync.WaitGroup
	bufferSize      int64 // 队列最大长度
	flushRetries    int
	flushInterval   time.Duration // 发送间隔
	pushOnce        sync.Once
	payloadListChan chan define.Payload

	batchSize float64 // 批次最大值
	count     float64 // 数据条数
}

// NewBackend :
func NewBackend(ctx context.Context, name string) (*Backend, error) {
	var (
		conf = config.FromContext(ctx)
		err  error
	)

	shipper := config.ShipperConfigFromContext(ctx)
	dbInfo := shipper.AsRedisCluster()
	auth := config.NewAuthInfo(shipper)
	key := dbInfo.GetKey()
	client := NewRedisClient(dbInfo, auth)
	// go-redis 的new cli 方法不返回err 需要自己去ping
	err = ClientPing(client)
	if err != nil || client == nil {
		logging.Errorf("new redis client failed:%v ", err)
		return nil, err
	}

	pipe := NewRedisPipeline(client)

	ctx, cancelFunc := context.WithCancel(ctx)

	return &Backend{
		BaseBackend:      define.NewBaseBackend(name),
		ProcessorMonitor: NewRedisBackendProcessorMonitor(config.PipelineConfigFromContext(ctx), key),
		key:              key,
		client:           client,
		pipe:             pipe,
		ctx:              ctx,
		cancelFunc:       cancelFunc,
		payloadListChan:  make(chan define.Payload),
		bufferSize:       conf.GetInt64(PayloadRedisBufferSize),
		batchSize:        conf.GetFloat64(PayloadRedisBatchSize),
		flushInterval:    conf.GetDuration(PayloadRedisFlushInterval),
		flushRetries:     conf.GetInt(PayloadRedisFlushRetries),
		count:            float64(0),
	}, err
}

func (b *Backend) SetETLRecordFields(f *define.ETLRecordFields) {}

// Push :
func (b *Backend) Push(d define.Payload, killChan chan<- error) {
	b.pushOnce.Do(func() {
		b.wg.Add(1)
		go func() {
			defer utils.RecoverError(func(e error) {
				logging.Errorf("push redis backend error %+v", e)
			})
			defer b.wg.Done()
			ticker := time.NewTicker(b.flushInterval)
		loop:
			for {
				select {
				case p, ok := <-b.payloadListChan:
					if !ok {
						break loop
					}
					err := b.handlePayload(p)
					if err != nil {
						b.CounterFails.Add(b.count)
						logging.Errorf("%v handle payload %v failed: %v", b, p, err)
					}
					if b.BatchFull() {
						err = b.SendMsg()
					}
					if err != nil {
						logging.Errorf("%v failed to insert: %v", b, err)
					}

				case <-ticker.C:
					err := b.SendMsg()
					if err != nil {
						logging.Errorf("%v failed to insert: %v", b, err)
					}
				case <-b.ctx.Done():
					break loop
				}
			}
			b.CleanUp()
			ticker.Stop()
		}()
	})

	b.payloadListChan <- d
}

// redisFullError :
func (b *Backend) redisFullError() error {
	llen := b.client.LLen(b.key).Val()
	if llen >= b.bufferSize {
		logging.Warnf("%v is full, llen = %v", b.key, llen)
		return errors.New("redis is full")
	}
	return nil
}

// StopInsert: 队列已满
func (b *Backend) StopInsert() error {
	// 随机查询长度 减少约1/3 的请求
	if rand.Intn(3) == 0 {
		return utils.ExponentialRetry(b.flushRetries, func() error {
			return b.redisFullError()
		})
	}
	return nil
}

// BatchFull : 批次是否满
func (b *Backend) BatchFull() bool {
	return b.count >= b.batchSize
}

// SendMsg:
func (b *Backend) SendMsg() error {
	defer func() {
		b.count = 0
	}()
	var err error
	// 队列未满
	if b.StopInsert() == nil {
		// redis pipeline 不支持重试机制
		res, err := b.pipe.Exec()
		if err != nil {
			logging.Errorf("%v push err: %v", b, err)
			return err
		}
		logging.Debugf("%v : %d data had been pushed to %v totally", b, len(res), b.key)
	} else {
		// 队列满且超过最大重试次数
		logging.Warnf("%v is full, pipeline close after %v times", b.key, b.flushRetries)
		b.CounterFails.Add(b.count)
		return errors.New("redis is full")
	}
	// 处理插入错误
	if err != nil {
		logging.Errorf("close because of %v", err)
		b.CounterFails.Add(b.count)
		return err
	}
	b.CounterSuccesses.Add(b.count)
	return err
}

// handlePayload : 数据格式转换,将数据推入pipeline
func (b *Backend) handlePayload(payload define.Payload) error {
	var message []byte

	err := payload.To(&message)
	if err != nil {
		logging.Warnf("%v load %#v error %v", b, payload, err)
		return err
	}
	_, err = b.pipe.LPush(b.key, message).Result()
	logging.Debugf("%v prepare to push %+v", b, payload)
	if err != nil {
		logging.Errorf("%v lpush %#v to %v error %v", b, payload, b.key, err)
		b.CounterFails.Add(1)
		return err
	}
	b.count++

	return nil
}

// CleanUp : 清空缓存队列数据
func (b *Backend) CleanUp() {
	for p := range b.payloadListChan {
		if err := b.handlePayload(p); err != nil {
			b.CounterFails.Add(b.count)
			logging.Errorf("%v clean up payload %v failed: %v", b, p, err)
		}
	}
	err := b.SendMsg()
	if err != nil {
		b.CounterFails.Add(b.count)
		logging.Errorf("send data %v failed: %v", b, err)
	}
	b.CounterSuccesses.Add(b.count)
}

// Close :
func (b *Backend) Close() error {
	b.cancelFunc()
	close(b.payloadListChan)
	b.wg.Wait()
	if err := b.pipe.Close(); err != nil {
		return errors.Errorf("%v close redis pipeline error : %+v", b, err)
	}
	if err := b.client.Close(); err != nil {
		return errors.Errorf("%v close redis client error : %+v", b, err)
	}
	return nil
}

func init() {
	define.RegisterBackend("redis", func(ctx context.Context, name string) (define.Backend, error) {
		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
		}
		if config.ShipperConfigFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "shipper config is empty")
		}
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewBackend(ctx, pipeConfig.FormatName(name))
	})
}
