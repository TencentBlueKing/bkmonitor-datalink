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
	"fmt"

	"github.com/go-redis/redis"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

var (
	TagCheckerOptionKey = "influxdb_tag_checker_option"

	TagStorageTypeMemory = "memory"
	TagStorageTypeRedis  = "redis"
)

type TagStorageRedisOption struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Password string `json:"string"`
	Database string `json:"database"`
}

type TagStorage interface {
	Put(key string) (bool, error)
	Has(key string) (bool, error)
	Count() (int64, error)
}

type TagStorageMemory struct {
	maxKeys int64
	data    map[string]bool
}

func (tsm *TagStorageMemory) Put(key string) (bool, error) {
	if int64(len(tsm.data)) >= tsm.maxKeys {
		return false, nil
	}

	tsm.data[key] = true
	return true, nil
}

func (tsm *TagStorageMemory) Has(key string) (bool, error) {
	_, ok := tsm.data[key]
	return ok, nil
}

func (tsm *TagStorageMemory) Count() (int64, error) {
	return int64(len(tsm.data)), nil
}

func NewTagStorageMemory(maxKeys int64) *TagStorageMemory {
	return &TagStorageMemory{
		maxKeys: maxKeys,
		data:    make(map[string]bool),
	}
}

type TagStorageRedis struct {
	key        string
	maxMembers int64
	client     *redis.Client
}

func (tsr *TagStorageRedis) Put(key string) (bool, error) {
	count, err := tsr.Count()
	if err != nil {
		return false, err
	}
	if count >= tsr.maxMembers {
		return false, nil
	}
	return true, tsr.client.SAdd(tsr.key, key).Err()
}

func (tsr *TagStorageRedis) Has(key string) (bool, error) {
	cmd := tsr.client.SIsMember(tsr.key, key)
	if err := cmd.Err(); err != nil {
		return false, err
	}
	return cmd.Val(), nil
}

func (tsr *TagStorageRedis) Count() (int64, error) {
	cmd := tsr.client.SCard(tsr.key)
	if err := cmd.Err(); err != nil {
		return 0, err
	}
	return cmd.Val(), nil
}

type TagStorageRedisConfig struct {
	Addr     string `json:"addr" mapstructure:"addr"`
	DB       int    `json:"db" mapstructure:"db"`
	Password string `json:"password" mapstructure:"password"`
}

func NewTagStorageRedis(maxKeys int64, config *TagStorageRedisConfig, key string) (*TagStorageRedis, error) {
	redisOption := &redis.Options{
		Addr:     config.Addr,
		Password: config.Password,
		DB:       config.DB,
	}
	client := redis.NewClient(redisOption)
	return &TagStorageRedis{
		key:        key,
		maxMembers: maxKeys,
		client:     client,
	}, nil
}

type RedisStorageOption struct {
	Key     string                `json:"key" mapstructure:"key"`
	Options TagStorageRedisConfig `json:"option" mapstructure:"option"`
}

type TagCheckerOption struct {
	MaxSeries     int64                  `json:"max_series" mapstructure:"max_series"`
	StorageType   string                 `json:"storage_type" mapstructure:"storage_type"`
	StorageOption map[string]interface{} `json:"storage_option" mapstrucutre:"storage_option"`
}

func (tco TagCheckerOption) NewTagCheckStorage() (storage TagStorage, err error) {
	if tco.StorageType == TagStorageTypeMemory {
		storage = NewTagStorageMemory(tco.MaxSeries)
	} else if tco.StorageType == TagStorageTypeRedis {
		storageOption := RedisStorageOption{}
		if err := mapstructure.Decode(tco.StorageOption, &storageOption); err != nil {
			return nil, fmt.Errorf("decode storage option failed, err: %+v", err)
		}
		storage, err = NewTagStorageRedis(tco.MaxSeries, &storageOption.Options, storageOption.Key)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("unexpected storage type: %s", tco.StorageType)
	}
	return storage, nil
}

// Processor :
type TagCheckProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	ClusterType string
	storage     TagStorage
}

func makeTagAsKey(record *Record) []string {
	key := ""
	for k, v := range record.Dimensions {
		key += fmt.Sprintf("\"%s\": \"%s\",", k, v)
	}
	keys := make([]string, 0)
	for metric := range record.Metrics {
		keys = append(keys, "{"+key+fmt.Sprintf("\"metric\":\"%s\"", metric)+"}")
	}
	return keys
}

func (p *TagCheckProcessor) storageKeyIfNotExist(keys []string) (bool, error) {
	for _, key := range keys {
		ok, err := p.storage.Has(key)
		if err != nil {
			return false, err
		}
		if ok {
			continue
		}
		ok, err = p.storage.Put(key)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (p *TagCheckProcessor) Process(payload define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	if p.ClusterType != BackendName {
		outputChan <- payload
		return
	}

	record := new(Record)
	err := payload.To(record)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v error %v dropped payload %+v", p, err, payload)
		return
	}

	keys := makeTagAsKey(record)
	ok, err := p.storageKeyIfNotExist(keys)
	if err != nil {
		logging.Errorf("%s check key exist failed, key: %+v, err: %+v", p, keys, err)
		p.CounterFails.Inc()
		killChan <- err
		return
	}
	if !ok {
		// TODO send a custom event
		logging.Warnf("%s has too much series", p)
		return
	}

	outputChan <- payload
	p.CounterSuccesses.Inc()
}

// NewProcessor :
func NewTagCheckProcessor(ctx context.Context, name string) *TagCheckProcessor {
	resultTableConfig := config.ResultTableConfigFromContext(ctx)
	clusterType := resultTableConfig.ShipperList[0].ClusterType
	processor := &TagCheckProcessor{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		ClusterType:       clusterType,
	}

	pipelineConfig := config.PipelineConfigFromContext(ctx)
	option, ok := pipelineConfig.Option[TagCheckerOptionKey].(string)
	if !ok {
		panic(fmt.Sprintf("config %s not set", TagCheckerOptionKey))
	}
	checkerOption := TagCheckerOption{}
	if err := json.Unmarshal([]byte(option), &checkerOption); err != nil {
		panic(fmt.Errorf("parse InfluxDBTagCheckerOption failed, err: %+v", err))
	}
	storage, err := checkerOption.NewTagCheckStorage()
	if err != nil {
		panic(err)
	}
	processor.storage = storage

	return processor
}

func init() {
	define.RegisterDataProcessor("influxdb_tab_checker", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewTagCheckProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
