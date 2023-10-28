// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"errors"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"go.uber.org/zap"
	"time"
)

type (
	Target string
	Action string

	SaveRequest struct {
		Target Target
		Action Action
		Data   any
	}

	QueryRequest struct {
		Target Target
		Data   any
	}

	ExistRequest struct {
		Target Target
		Key    string
	}
)

const (
	Cache       Target = "cache"
	TraceEs     Target = "traceEs"
	SaveEs      Target = "saveEs"
	BloomFilter Target = "bloom-filter"
)

type ProxyOptions struct {
	// workerCount Number of workers processing SaveRequest.
	workerCount int

	saveHoldDuration time.Duration
	saveHoldMaxCount int

	cacheBackend     CacheType
	redisCacheConfig RedisCacheOptions
	bloomConfig      BloomOptions

	traceEsConfig EsOptions
	saveEsConfig  EsOptions

	// saveReqBufferSize Number of queue capacity that hold SaveRequest
	saveReqBufferSize int
}

type ProxyOption func(options *ProxyOptions)

// WorkerCount The number of concurrent storage requests accepted simultaneously
func WorkerCount(c int) ProxyOption {
	return func(options *ProxyOptions) {
		options.workerCount = c
	}
}

// SaveHoldDuration unit: ms.
// Storage does not process the SaveRequest immediately upon receipt,
// it waits for the conditions(SaveHoldDuration + SaveHoldMaxCount).
// Condition 1: If the wait time > SaveHoldDuration, it will be executed
func SaveHoldDuration(c int) ProxyOption {
	return func(options *ProxyOptions) {
		options.saveHoldDuration = time.Duration(c) * time.Millisecond
	}
}

// SaveHoldMaxCount Storage does not process the SaveRequest immediately upon receipt,
// it waits for the conditions(SaveHoldDuration + SaveHoldMaxCount).
// Condition 2: If the request count > SaveHoldMaxCount, it will be executed
func SaveHoldMaxCount(c int) ProxyOption {
	return func(options *ProxyOptions) {
		options.saveHoldMaxCount = c
	}
}

// CacheBackend Specifies the type of cache
func CacheBackend(t CacheType) ProxyOption {
	return func(options *ProxyOptions) {
		options.cacheBackend = t
	}
}

// CacheRedisConfig Redis cache configuration. It is valid only when CacheBackend == CacheTypeRedis
func CacheRedisConfig(opts ...RedisCacheOption) ProxyOption {
	return func(options *ProxyOptions) {
		redisOpt := RedisCacheOptions{}
		for _, setter := range opts {
			setter(&redisOpt)
		}
		options.redisCacheConfig = redisOpt
	}
}

// RedisBloomConfig If this configuration is used, redis must support redis-bloom.
func RedisBloomConfig(opts ...BloomOption) ProxyOption {
	return func(options *ProxyOptions) {
		bloomOpts := BloomOptions{}
		for _, setter := range opts {
			setter(&bloomOpts)
		}
		options.bloomConfig = bloomOpts
	}
}

// TraceEsConfig Elasticsearch config of storage
func TraceEsConfig(opts ...EsOption) ProxyOption {
	return func(options *ProxyOptions) {
		esOpts := EsOptions{}
		for _, setter := range opts {
			setter(&esOpts)
		}
		options.traceEsConfig = esOpts
	}
}

func SaveEsConfig(opts ...EsOption) ProxyOption {
	return func(options *ProxyOptions) {
		esOpts := EsOptions{}
		for _, setter := range opts {
			setter(&esOpts)
		}
		options.saveEsConfig = esOpts
	}
}

// SaveReqBufferSize Number of storage chan
func SaveReqBufferSize(s int) ProxyOption {
	return func(options *ProxyOptions) {
		options.saveReqBufferSize = s
	}
}

// Proxy storage backend proxy.
type Proxy struct {
	config ProxyOptions

	traceEs     *esStorage
	saveEs      *esStorage
	cache       CacheOperator
	bloomFilter BloomOperator

	ctx             context.Context
	saveRequestChan chan SaveRequest
}

func (p *Proxy) Run(errorReceiveChan chan<- error) {
	logger.Infof("StorageProxy started.")
	for i := 0; i < p.config.workerCount; i++ {
		go p.ReceiveSaveRequest(errorReceiveChan)
	}
}

func (p *Proxy) SaveRequest() chan<- SaveRequest {
	return p.saveRequestChan
}

func (p *Proxy) ReceiveSaveRequest(errorReceiveChan chan<- error) {
	defer runtimex.HandleCrashToChan(errorReceiveChan)

	ticker := time.NewTicker(p.config.saveHoldDuration)
	esSaveData := make([]EsStorageData, 0, p.config.saveHoldMaxCount)
	cacheSaveData := make([]CacheStorageData, 0, p.config.saveHoldMaxCount)
loop:
	for {
		select {
		case r := <-p.saveRequestChan:
			switch r.Target {
			case SaveEs:
				esSaveData = append(esSaveData, r.Data.(EsStorageData))
				if len(esSaveData) >= p.config.saveHoldMaxCount {
					// todo 是否需要满时动态调整
					err := p.saveEs.SaveBatch(esSaveData)
					esSaveData = make([]EsStorageData, 0, p.config.saveHoldMaxCount)
					if err != nil {
						logger.Errorf("[MAX TRIGGER] Failed to save %d pieces of data to ES, cause: %s", len(esSaveData), err)
					}
				}
			case Cache:
				cacheSaveData = append(cacheSaveData, r.Data.(CacheStorageData))
				if len(cacheSaveData) >= p.config.saveHoldMaxCount {
					err := p.cache.SaveBatch(cacheSaveData)
					cacheSaveData = make([]CacheStorageData, 0, p.config.saveHoldMaxCount)
					if err != nil {
						logger.Errorf("[MAX TRIGGER] Failed to save %d pieces of data to CACHE, cause: %s", len(cacheSaveData), err)
					}
				}
			case BloomFilter:
				// Bloom-filter needs to be added immediately,
				// otherwise it may not be added and cause an error in judgment.
				data := r.Data.(BloomStorageData)
				if err := p.bloomFilter.Add(data); err != nil {
					logger.Errorf("Bloom Filter add key: %s failed, error: %s", data.Key, err)
				}
			default:
				logger.Warnf("An invalid storage SAVE request was received: %s", r.Target)
			}
		case <-ticker.C:
			if len(esSaveData) != 0 {
				err := p.saveEs.SaveBatch(esSaveData)
				esSaveData = make([]EsStorageData, 0, p.config.saveHoldMaxCount)
				if err != nil {
					logger.Errorf("[TICKER TRIGGER] Failed to save %d pieces of data to ES, cause: %s", len(esSaveData), err)
				}
			}
			if len(cacheSaveData) != 0 {
				err := p.cache.SaveBatch(cacheSaveData)
				cacheSaveData = make([]CacheStorageData, 0, p.config.saveHoldMaxCount)
				if err != nil {
					logger.Errorf("[TICKER TRIGGER] Failed to save %d pieces of data to CACHE, cause: %s", len(cacheSaveData), err)
				}
			}
		case <-p.ctx.Done():
			logger.Infof("Storage proxy receive stop signal, data saving stopped.")
			break loop
		}
	}
}

func (p *Proxy) Query(queryRequest QueryRequest) (any, error) {
	switch queryRequest.Target {
	case TraceEs:
		return p.traceEs.Query(queryRequest.Data)
	case SaveEs:
		return p.saveEs.Query(queryRequest.Data)
	case Cache:
		return p.cache.Query(queryRequest.Data.(string))
	default:
		info := fmt.Sprintf("An invalid storage QUERY request was received: %s", queryRequest.Target)
		logger.Warnf(info)
		return nil, errors.New(info)
	}
}

func (p *Proxy) Exist(req ExistRequest) (bool, error) {
	switch req.Target {
	case BloomFilter:
		return p.bloomFilter.Exist(req.Key)
	default:
		logger.Warnf("Exist method does not support type: %s, it will return false", req.Target)
		return false, nil
	}
}

func NewProxyInstance(ctx context.Context, options ...ProxyOption) (*Proxy, error) {
	opt := ProxyOptions{}
	for _, setter := range options {
		setter(&opt)
	}
	traceEsInstance, err := newEsStorage(opt.traceEsConfig)
	if err != nil {
		return nil, err
	}
	saveEsInstance, err := newEsStorage(opt.saveEsConfig)
	if err != nil {
		return nil, err
	}

	// create cache storage
	var cache CacheOperator
	if opt.cacheBackend == CacheTypeRedis {
		cache, err = newRedisCache(opt.redisCacheConfig)
		if err != nil {
			return nil, err
		}
	} else {
		cache, err = newMemoryCache()
		if err != nil {
			return nil, err
		}
	}

	// todo 由于部署环境可能不支持redis-bloom 故统一使用内存方式进行
	bloomFilter, err := newMemoryCacheBloomClient(opt.bloomConfig)
	if err != nil {
		return nil, err
	}

	return &Proxy{
		config:          opt,
		traceEs:         traceEsInstance,
		saveEs:          saveEsInstance,
		cache:           cache,
		bloomFilter:     bloomFilter,
		ctx:             ctx,
		saveRequestChan: make(chan SaveRequest, opt.saveReqBufferSize),
	}, nil
}

var logger = monitorLogger.With(
	zap.String("name", "storage"),
)
