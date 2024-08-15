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
	"time"

	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
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
	Prometheus  Target = "prometheus"
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

	prometheusWriterConfig PrometheusWriterOptions
	metricsConfig          MetricConfigOptions

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
func SaveHoldDuration(c time.Duration) ProxyOption {
	return func(options *ProxyOptions) {
		options.saveHoldDuration = c
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

func BloomConfig(opts ...BloomOption) ProxyOption {
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

func PrometheusWriterConfig(opts ...PrometheusWriterOption) ProxyOption {
	return func(options *ProxyOptions) {
		writerOpts := PrometheusWriterOptions{}
		for _, setter := range opts {
			setter(&writerOpts)
		}
		options.prometheusWriterConfig = writerOpts
	}
}

func MetricsConfig(opts ...MetricConfigOption) ProxyOption {
	return func(options *ProxyOptions) {
		metricOpts := MetricConfigOptions{}
		for _, setter := range opts {
			setter(&metricOpts)
		}
		options.metricsConfig = metricOpts
	}
}

// SaveReqBufferSize Number of storage chan
func SaveReqBufferSize(s int) ProxyOption {
	return func(options *ProxyOptions) {
		options.saveReqBufferSize = s
	}
}

type Backend interface {
	Run(errorReceiveChan chan<- error)
	SaveRequest() chan<- SaveRequest
	ReceiveSaveRequest(errorReceiveChan chan<- error)
	Query(queryRequest QueryRequest) (any, error)
	Exist(req ExistRequest) (bool, error)
	GetClient(t Target) any
}

// Proxy storage backend proxy.
type Proxy struct {
	dataId string

	config ProxyOptions

	traceEs                  *esStorage
	saveEs                   *esStorage
	cache                    CacheOperator
	bloomFilter              BloomOperator
	prometheusMetricsHandler *MetricDimensionsHandler

	ctx             context.Context
	saveRequestChan chan SaveRequest
	logger          monitorLogger.Logger
}

func (p *Proxy) Run(errorReceiveChan chan<- error) {
	p.logger.Infof("StorageProxy started with %d workers", p.config.workerCount)
	for i := 0; i < p.config.workerCount; i++ {
		go p.ReceiveSaveRequest(errorReceiveChan)
	}
	go p.watchSaveRequestChan()
}

func (p *Proxy) SaveRequest() chan<- SaveRequest {
	return p.saveRequestChan
}

func (p *Proxy) watchSaveRequestChan() {
	for {
		select {
		case <-p.ctx.Done():
			// prevent repeated close under multithreading
			close(p.saveRequestChan)
			p.logger.Infof("close storage saveRequestChan")
			return
		}
	}
}

func (p *Proxy) ReceiveSaveRequest(errorReceiveChan chan<- error) {
	defer runtimex.HandleCrashToChan(errorReceiveChan)

	ticker := time.NewTicker(p.config.saveHoldDuration)
	esSaveData := make([]EsStorageData, 0, p.config.saveHoldMaxCount)
	cacheSaveData := make([]CacheStorageData, 0, p.config.saveHoldMaxCount)
loop:
	for {
		select {
		case r, isOpen := <-p.saveRequestChan:
			if !isOpen {
				p.logger.Infof("saveRequestChan close, return")
				return
			}
			switch r.Target {
			case SaveEs:
				item := r.Data.(EsStorageData)

				esSaveData = append(esSaveData, item)
				if len(esSaveData) >= p.config.saveHoldMaxCount {
					err := p.saveEs.SaveBatch(esSaveData)
					metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageSaveEs, metrics.OperateSave)
					metrics.RecordApmPreCalcSaveStorageTotal(p.dataId, metrics.StorageSaveEs, len(esSaveData))
					if err != nil {
						p.logger.Errorf("[MAX TRIGGER] Failed to save %d pieces of data to ES, cause: %s", len(esSaveData), err)
						metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.SaveEsFailed)
					}
					esSaveData = make([]EsStorageData, 0, p.config.saveHoldMaxCount)
				}
			case Cache:
				item := r.Data.(CacheStorageData)
				metrics.RecordApmPreCalcOperateStorageCount(item.DataId, metrics.StorageCache, metrics.OperateSave)

				cacheSaveData = append(cacheSaveData, item)
				if len(cacheSaveData) >= p.config.saveHoldMaxCount {
					err := p.cache.SaveBatch(cacheSaveData)
					metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageCache, metrics.OperateSave)
					metrics.RecordApmPreCalcSaveStorageTotal(p.dataId, metrics.StorageCache, len(cacheSaveData))
					if err != nil {
						p.logger.Errorf("[MAX TRIGGER] Failed to save %d pieces of data to CACHE, cause: %s", len(cacheSaveData), err)
						metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.SaveCacheFailed)
					}
					cacheSaveData = make([]CacheStorageData, 0, p.config.saveHoldMaxCount)
				}
			case BloomFilter:
				// Bloom-filter needs to be added immediately,
				// otherwise it may not be added and cause an error in judgment.
				item := r.Data.(BloomStorageData)
				metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageBloomFilter, metrics.OperateSave)
				metrics.RecordApmPreCalcSaveStorageTotal(p.dataId, metrics.StorageBloomFilter, 1)
				if err := p.bloomFilter.Add(item); err != nil {
					p.logger.Errorf("Bloom Filter add key: %s failed, error: %s", item.Key, err)
					metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.SaveBloomFilterFailed)
				}
			case Prometheus:
				// Metrics of prometheus is directly handed over to handler (Sending is triggered by handler)
				item := r.Data.(PrometheusStorageData)
				p.prometheusMetricsHandler.Add(item)
			default:
				p.logger.Warnf("An invalid storage SAVE request was received: %s", r.Target)
			}
		case <-ticker.C:
			if len(esSaveData) != 0 {
				err := p.saveEs.SaveBatch(esSaveData)
				metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageSaveEs, metrics.OperateSave)
				metrics.RecordApmPreCalcSaveStorageTotal(p.dataId, metrics.StorageSaveEs, len(esSaveData))
				if err != nil {
					p.logger.Errorf("[TICKER TRIGGER] Failed to save %d pieces of data to ES, cause: %s", len(esSaveData), err)
					metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.SaveEsFailed)
				}
				esSaveData = make([]EsStorageData, 0, p.config.saveHoldMaxCount)
			}
			if len(cacheSaveData) != 0 {
				err := p.cache.SaveBatch(cacheSaveData)
				metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageCache, metrics.OperateSave)
				metrics.RecordApmPreCalcSaveStorageTotal(p.dataId, metrics.StorageCache, len(cacheSaveData))
				if err != nil {
					p.logger.Errorf("[TICKER TRIGGER] Failed to save %d pieces of data to CACHE, cause: %s", len(cacheSaveData), err)
					metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.SaveCacheFailed)
				}
				cacheSaveData = make([]CacheStorageData, 0, p.config.saveHoldMaxCount)
			}
		case <-p.ctx.Done():
			ticker.Stop()
			p.cache.Close()
			p.prometheusMetricsHandler.Close()
			break loop
		}
	}

	p.logger.Infof("Storage proxy receive stop signal, data saving stopped.")
}

func (p *Proxy) Query(queryRequest QueryRequest) (any, error) {
	switch queryRequest.Target {
	case TraceEs:
		metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageTraceEs, metrics.OperateQuery)
		return p.traceEs.Query(queryRequest.Data)
	case SaveEs:
		metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageSaveEs, metrics.OperateQuery)
		return p.saveEs.Query(queryRequest.Data)
	case Cache:
		metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageCache, metrics.OperateQuery)
		return p.cache.Query(queryRequest.Data.(string))
	default:
		info := fmt.Sprintf("An invalid storage QUERY request was received: %s", queryRequest.Target)
		p.logger.Warnf(info)
		return nil, errors.New(info)
	}
}

func (p *Proxy) Exist(req ExistRequest) (bool, error) {
	switch req.Target {
	case BloomFilter:
		metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StorageBloomFilter, metrics.OperateQuery)
		return p.bloomFilter.Exist(req.Key)
	default:
		p.logger.Warnf("Exist method does not support type: %s, it will return false", req.Target)
		return false, nil
	}
}

func (p *Proxy) GetClient(t Target) any {
	switch t {
	case TraceEs:
		return p.traceEs.client
	case SaveEs:
		return p.saveEs.client
	default:
		return nil
	}
}

func NewProxyInstance(dataId string, ctx context.Context, options ...ProxyOption) (*Proxy, error) {
	opt := ProxyOptions{}
	for _, setter := range options {
		setter(&opt)
	}
	traceEsInstance, err := newEsStorage(ctx, opt.traceEsConfig)
	if err != nil {
		return nil, err
	}
	saveEsInstance, err := newEsStorage(ctx, opt.saveEsConfig)
	if err != nil {
		return nil, err
	}

	// create cache storage
	var cache CacheOperator
	if opt.cacheBackend == CacheTypeRedis {
		cache, err = newRedisCache(ctx, opt.redisCacheConfig)
		if err != nil {
			return nil, err
		}
	} else {
		cache, err = newMemoryCache()
		if err != nil {
			return nil, err
		}
	}

	bloomFilter, err := newLayersCapDecreaseBloomClient(dataId, ctx, opt.bloomConfig)
	if err != nil {
		return nil, err
	}

	return &Proxy{
		dataId:                   dataId,
		config:                   opt,
		traceEs:                  traceEsInstance,
		saveEs:                   saveEsInstance,
		cache:                    cache,
		bloomFilter:              bloomFilter,
		prometheusMetricsHandler: NewMetricDimensionHandler(ctx, dataId, opt.prometheusWriterConfig, opt.metricsConfig),
		ctx:                      ctx,
		saveRequestChan:          make(chan SaveRequest, opt.saveReqBufferSize),
		logger:                   monitorLogger.With(zap.String("name", "storage"), zap.String("dataId", dataId)),
	}, nil
}
