// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/oleiade/reflections"
	"github.com/pkg/errors"
	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	remotewrite "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Builder Pre-Calculate default configuration builder
type Builder interface {
	WithContext(context.Context, context.CancelFunc) Builder
	WithNotifierConfig(notifier.Options) Builder
	WithWindowRuntimeConfig(window.RuntimeConfig) Builder
	WithDistributiveWindowConfig(window.DistributiveWindowOptions) Builder
	WithProcessorConfig(window.ProcessorOptions) Builder
	WithStorageConfig(storage.ProxyOptions) Builder
	WithMetricReport(SidecarOptions) Builder
	Build() PreCalculateProcessor
}

type PreCalculateProcessor interface {
	PreCalculateProcessorStandLone

	Start(stopParentContext context.Context, errorReceiveChan chan<- error, payload []byte)
	GetTaskDimension(payload []byte) string
	Run(errorChan chan<- error)

	StartByDataId(ctx context.Context, startInfo StartInfo, errorReceiveChan chan<- error)
}

var (
	preCalculateOnce     sync.Once
	preCalculateInstance *Precalculate
)

type StartInfo struct {
	DataId string         `json:"data_id"`
	Config map[string]any `json:"config"`
}

type Precalculate struct {
	// ctx Root context
	ctx    context.Context
	cancel context.CancelFunc

	// defaultConfig is the global default configuration for pre-calculate.
	// If a dataId needs to be configured independently, you can override it using config in the Start method
	defaultConfig PrecalculateOption

	readySignalChan chan readySignal

	httpTransport *http.Transport
}

type PrecalculateOption struct {
	// window-specific-config
	WindowConfig    window.DistributiveWindowOptions `json:"windowConfig"`
	RuntimeConfig   window.RuntimeConfig             `json:"runtimeConfig"`
	NotifierConfig  notifier.Options                 `json:"notifierConfig"`
	ProcessorConfig window.ProcessorOptions          `json:"processorConfig"`
	StorageConfig   storage.ProxyOptions             `json:"storageConfig"`
	SidecarConfig   SidecarOptions                   `json:"sidecarConfig"`
}

type readySignal struct {
	ctx              context.Context
	startInfo        StartInfo
	config           PrecalculateOption
	errorReceiveChan chan<- error
}

func (p *Precalculate) WithContext(ctx context.Context, cancel context.CancelFunc) Builder {
	p.ctx = ctx
	p.cancel = cancel
	return p
}

func (p *Precalculate) WithNotifierConfig(options notifier.Options) Builder {
	p.defaultConfig.NotifierConfig = options
	return p
}

func (p *Precalculate) WithWindowRuntimeConfig(options window.RuntimeConfig) Builder {
	p.defaultConfig.RuntimeConfig = options
	return p
}

func (p *Precalculate) WithDistributiveWindowConfig(options window.DistributiveWindowOptions) Builder {
	p.defaultConfig.WindowConfig = options
	return p
}

func (p *Precalculate) WithProcessorConfig(options window.ProcessorOptions) Builder {
	p.defaultConfig.ProcessorConfig = options
	return p
}

func (p *Precalculate) WithStorageConfig(options storage.ProxyOptions) Builder {
	p.defaultConfig.StorageConfig = options
	return p
}

func (p *Precalculate) WithMetricReport(options SidecarOptions) Builder {
	p.defaultConfig.SidecarConfig = options
	return p
}

func (p *Precalculate) Build() PreCalculateProcessor {

	preCalculateOnce.Do(func() {
		preCalculateInstance = p
	})

	return preCalculateInstance
}

func NewPrecalculate() Builder {
	// Use the same http.Transport of reporting to avoid excessive connections
	httpMetricTransport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 10 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &Precalculate{readySignalChan: make(chan readySignal), httpTransport: httpMetricTransport}
}

func (p *Precalculate) GetTaskDimension(payload []byte) string {
	var startInfo StartInfo
	if err := jsonx.Unmarshal(payload, &startInfo); err != nil {
		logger.Errorf(
			"failed to start APM-Precalculate as parse value to StartInfo error, value: %s. error: %s",
			payload, err,
		)
		return string(payload)
	}
	return startInfo.DataId
}

func (p *Precalculate) Start(runInstanceCtx context.Context, errorReceiveChan chan<- error, payload []byte) {

	var startInfo StartInfo
	if err := jsonx.Unmarshal(payload, &startInfo); err != nil {
		errorReceiveChan <- fmt.Errorf(
			"failed to start APM-Precalculate as parse value to StartInfo error, value: %s. error: %s",
			payload, err)
		return
	}

	p.StartByDataId(runInstanceCtx, startInfo, errorReceiveChan)
}

func (p *Precalculate) StartByDataId(runInstanceCtx context.Context, startInfo StartInfo, errorReceiveChan chan<- error) {
	ticker := time.NewTicker(5 * time.Second)
loop:
	for {
		select {
		case <-ticker.C:
			if err := core.GetMetadataCenter().AddDataId(startInfo.DataId); err != nil {
				apmLogger.Errorf(
					"[StartByDataId] Failed to start the pre-calculation with dataId: %s, it will not be executed. error: %s",
					startInfo.DataId, err,
				)
				continue
			}
			config := p.defaultConfig
			if len(startInfo.Config) > 0 {
				taskConfig := PrecalculateOption{}
				var updateKeys []string
				updateKeys, err := p.convertMappingToConfig(&taskConfig, startInfo.Config)
				if err != nil {
					errorReceiveChan <- fmt.Errorf("[StartByDataId] failed to convert json to config(value: %+v), error: %s", startInfo.Config, err)
					return
				} else {
					configP, err := mergeConfigs(&config, &taskConfig, updateKeys)
					config = *configP
					if err != nil {
						errorReceiveChan <- fmt.Errorf("[StartByDataId] failed to merge config, error: %s", err)
						return
					}
				}
			}

			p.readySignalChan <- readySignal{
				ctx:              runInstanceCtx,
				startInfo:        startInfo,
				config:           config,
				errorReceiveChan: errorReceiveChan,
			}
			return
		case <-runInstanceCtx.Done():
			logger.Infof("StartByDataId stopped.")
			ticker.Stop()
			break loop
		}
	}
}

func mergeConfigs[T any](dst, src T, updateKeys []string) (T, error) {
	dstPointValue := reflect.ValueOf(dst)
	dstValue := dstPointValue.Elem()
	dstType := dstValue.Type()
	structName := dstType.Name()

	for i := 0; i < dstType.NumField(); i++ {
		fieldName := dstType.Field(i).Name
		fieldKind, _ := reflections.GetFieldKind(dst, fieldName)
		if fieldKind == reflect.Struct {
			dstFieldIns, err := reflections.GetField(dst, fieldName)
			if err != nil {
				return dst, err
			}
			srcFieldIns, err := reflections.GetField(src, fieldName)
			if err != nil {
				return dst, err
			}
			// 没举出所有配置类型 如果在这里没有 case 则代表不能配置
			// 由于 go 语言限制无法做到使用 reflect 来实现 (reflect 都是基于 interface 的在嵌套结构里面无法转换为具体类型)
			switch dstFieldIns.(type) {
			case window.DistributiveWindowOptions:
				dstSpec := dstFieldIns.(window.DistributiveWindowOptions)
				srcSpec := srcFieldIns.(window.DistributiveWindowOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case window.RuntimeConfig:
				dstSpec := dstFieldIns.(window.RuntimeConfig)
				srcSpec := srcFieldIns.(window.RuntimeConfig)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case notifier.Options:
				dstSpec := dstFieldIns.(notifier.Options)
				srcSpec := srcFieldIns.(notifier.Options)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case window.ProcessorOptions:
				dstSpec := dstFieldIns.(window.ProcessorOptions)
				srcSpec := srcFieldIns.(window.ProcessorOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case storage.ProxyOptions:
				dstSpec := dstFieldIns.(storage.ProxyOptions)
				srcSpec := srcFieldIns.(storage.ProxyOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case SidecarOptions:
				dstSpec := dstFieldIns.(SidecarOptions)
				srcSpec := srcFieldIns.(SidecarOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case storage.BloomOptions:
				dstSpec := dstFieldIns.(storage.BloomOptions)
				srcSpec := srcFieldIns.(storage.BloomOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case remotewrite.PrometheusWriterOptions:
				dstSpec := dstFieldIns.(remotewrite.PrometheusWriterOptions)
				srcSpec := srcFieldIns.(remotewrite.PrometheusWriterOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case storage.MetricConfigOptions:
				dstSpec := dstFieldIns.(storage.MetricConfigOptions)
				srcSpec := srcFieldIns.(storage.MetricConfigOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case storage.MemoryBloomOptions:
				dstSpec := dstFieldIns.(storage.MemoryBloomOptions)
				srcSpec := srcFieldIns.(storage.MemoryBloomOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case storage.OverlapBloomOptions:
				dstSpec := dstFieldIns.(storage.OverlapBloomOptions)
				srcSpec := srcFieldIns.(storage.OverlapBloomOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case storage.LayersBloomOptions:
				dstSpec := dstFieldIns.(storage.LayersBloomOptions)
				srcSpec := srcFieldIns.(storage.LayersBloomOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			case storage.LayersCapDecreaseBloomOptions:
				dstSpec := dstFieldIns.(storage.LayersCapDecreaseBloomOptions)
				srcSpec := srcFieldIns.(storage.LayersCapDecreaseBloomOptions)
				nestedFieldConfig, err := mergeConfigs(&dstSpec, &srcSpec, updateKeys)
				if err != nil {
					return dst, err
				}
				if err = reflections.SetField(dst, fieldName, *nestedFieldConfig); err != nil {
					return dst, err
				}
			}
			continue
		}
		key := fmt.Sprintf("%s.%s", structName, fieldName)
		if slices.Contains(updateKeys, key) {
			srcFieldValue, _ := reflections.GetField(src, fieldName)
			if err := reflections.SetField(dst, fieldName, srcFieldValue); err != nil {
				return dst, err
			}
		}
	}

	return dst, nil
}

func (p *Precalculate) convertMappingToConfig(resTemplate any, mapping map[string]any) ([]string, error) {
	var updateKeys []string
	v := reflect.ValueOf(resTemplate)
	elem := v.Elem()
	templateType := elem.Type()
	structName := templateType.Name()
	for key, value := range mapping {
		field, found := templateType.FieldByNameFunc(func(s string) bool {
			return strings.ToLower(s) == strings.ToLower(key)
		})
		if !found {
			return updateKeys, fmt.Errorf("[Precalculate] find invalid config key: %s from json", key)
		}
		fieldType := elem.FieldByName(field.Name)

		if fieldType.IsValid() && fieldType.CanSet() {
			switch fieldType.Kind() {
			case reflect.Int:
				if val, ok := value.(float64); ok {
					fieldType.SetInt(int64(val))
				} else {
					return updateKeys, fmt.Errorf("[Precalculate] find invalid config value: %s of key: %s(type: %s) from json", value, key, fieldType.Kind())
				}
			case reflect.Bool:
				if val, ok := value.(bool); ok {
					fieldType.SetBool(val)
				} else {
					return updateKeys, fmt.Errorf("[Precalculate] find invalid config value: %s of key: %s(type: %s) from json", value, key, fieldType.Kind())
				}
			case reflect.Map:
				if val, ok := value.(map[string]any); ok {
					fieldType.Set(reflect.MakeMap(fieldType.Type()))
					for k, v := range val {
						vStr := v.(string)
						fieldType.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(vStr))
					}
				} else {
					return updateKeys, fmt.Errorf("[Precalculate] find invalid config value: %s of key: %s(type: %s) from json", value, key, fieldType.Kind())
				}
			case reflect.Slice:
				elemType := fieldType.Type().Elem().Kind()
				if elemType == reflect.Float64 {
					if val, ok := value.([]any); ok {
						slice := reflect.MakeSlice(fieldType.Type(), len(val), len(val))
						for i, v := range val {
							if f, ok := v.(float64); ok {
								slice.Index(i).SetFloat(f)
							}
						}
						fieldType.Set(slice)
					} else {
						logger.Warnf("[Precalculate] can not convert value: %s to array of key: %s from json", value, key)
					}
				}
				if elemType == reflect.Int {

					if val, ok := value.([]any); ok {
						slice := reflect.MakeSlice(fieldType.Type(), len(val), len(val))
						for i, v := range val {
							if f, ok := v.(int64); ok {
								slice.Index(i).SetInt(f)
							}
						}
						fieldType.Set(slice)
					}
				}
			case reflect.String:
				if val, ok := value.(string); ok {
					fieldType.SetString(val)
				}
			case reflect.Int64:
				// All types of int64 used in apm configuration are time.Duration
				if val, ok := value.(string); ok {
					d, err := time.ParseDuration(val)
					if err != nil {
						return updateKeys, fmt.Errorf("[Precalculate] failed to convert value: %s to duration of key: %s from json", value, key)
					}
					fieldType.SetInt(int64(d))
				} else if val, ok := value.(int64); ok {
					fieldType.SetInt(val)
				} else {
					return updateKeys, fmt.Errorf("[Precalculate] not supported field type(value: %s) of key: %s from json", value, key)
				}
			case reflect.Struct:
				nestedFieldInstance := reflect.New(fieldType.Type()).Interface()
				if nestedMap, ok := value.(map[string]any); ok {
					nestedUpdateKeys, err := p.convertMappingToConfig(nestedFieldInstance, nestedMap)
					if err != nil {
						return updateKeys, err
					}
					fieldType.Set(reflect.ValueOf(nestedFieldInstance).Elem())
					updateKeys = append(updateKeys, nestedUpdateKeys...)
				}
			default:
				logger.Warnf("[Precalculate] find not supported type: %s of field: %+v", fieldType.Kind(), field)
				continue
			}
			updateKeys = append(updateKeys, fmt.Sprintf("%s.%s", structName, field.Name))
		} else {
			return updateKeys, fmt.Errorf("[Precalculate] find invalid config key: %s from json", key)
		}
	}
	return updateKeys, nil
}

func (p *Precalculate) Run(runSuccess chan<- error) {
	if err := core.CreateMetadataCenter(); err != nil {
		runSuccess <- err
		return
	}
	apmLogger.Infof("Pre-calculate is running...")
	runSuccess <- nil
loop:
	for {
		select {
		case signal := <-p.readySignalChan:
			apmLogger.Infof("Pre-calculation with dataId: %s was received.", signal.startInfo.DataId)
			p.launch(signal.ctx, signal.startInfo, signal.config, signal.errorReceiveChan)
		case <-p.ctx.Done():
			apmLogger.Info("Precalculate[MAIN] received the stop signal.")
			break loop
		}
	}
}

func (p *Precalculate) launch(
	runInstanceCtx context.Context, startInfo StartInfo, conf PrecalculateOption, errorReceiveChan chan<- error,
) {
	defer runtimex.HandleCrashToChan(errorReceiveChan)

	runInstance := RunInstance{startInfo: startInfo, config: conf, ctx: runInstanceCtx, errorReceiveChan: errorReceiveChan}

	messageChan, err := runInstance.startNotifier()
	if err != nil {
		errorReceiveChan <- fmt.Errorf("failed to start notifier, dataId: %s, error: %s", startInfo.DataId, err)
		return
	}

	saveReqChan, err := runInstance.startStorageBackend()
	if err != nil {
		errorReceiveChan <- fmt.Errorf("failed to start storage backend, dataId: %s, error: %s", startInfo.DataId, err)
		return
	}

	runInstance.startWindowHandler(messageChan, saveReqChan)
	runInstance.startRuntimeCollector()
	go runInstance.watchConsulConfigUpdate(errorReceiveChan)
	apmLogger.Infof("dataId: %s launch successfully", startInfo.DataId)
}

type RunInstance struct {
	ctx context.Context

	startInfo        StartInfo
	config           PrecalculateOption
	errorReceiveChan chan<- error

	notifier      notifier.Notifier
	windowHandler window.Operation
	proxy         *storage.Proxy

	RuntimeCollector SidecarCollector
}

func (p *RunInstance) startNotifier() (<-chan []window.StandardSpan, error) {
	kafkaConfig := core.GetMetadataCenter().GetKafkaConfig(p.startInfo.DataId)
	groupId := "go-apm-pre-calculate-consumer-group"

	n, err := notifier.NewNotifier(
		notifier.KafkaNotifier,
		p.startInfo.DataId,
		notifier.Options{
			ChanBufferSize: p.config.NotifierConfig.ChanBufferSize,
			Ctx:            p.ctx,
			KafkaConfig: notifier.KafkaConfig{
				KafkaGroupId:  groupId,
				KafkaHost:     kafkaConfig.Host,
				KafkaUsername: kafkaConfig.Username,
				KafkaPassword: kafkaConfig.Password,
				KafkaTopic:    kafkaConfig.Topic,
			},
			Qps: p.config.NotifierConfig.Qps,
		},
	)
	if err != nil {
		return nil, err
	}

	p.notifier = n
	go n.Start(p.errorReceiveChan)
	return n.Spans(), nil
}

func (p *RunInstance) startWindowHandler(messageChan <-chan []window.StandardSpan, saveReqChan chan<- storage.SaveRequest) {

	processor := window.NewProcessor(p.ctx, p.startInfo.DataId, p.proxy, p.config.ProcessorConfig)

	operation := window.Operation{
		Operator: window.NewDistributiveWindow(
			p.startInfo.DataId,
			p.ctx,
			processor,
			saveReqChan,
			p.config.WindowConfig,
		),
	}
	operation.Run(messageChan, p.errorReceiveChan, p.config.RuntimeConfig)

	p.windowHandler = operation
}

func (p *RunInstance) startStorageBackend() (chan<- storage.SaveRequest, error) {
	traceEsConfig := core.GetMetadataCenter().GetTraceEsConfig(p.startInfo.DataId)
	saveEsConfig := core.GetMetadataCenter().GetSaveEsConfig(p.startInfo.DataId)

	proxy, err := storage.NewProxyInstance(
		p.startInfo.DataId,
		p.ctx,
		storage.ProxyOptions{
			WorkerCount:         p.config.StorageConfig.WorkerCount,
			SaveHoldMaxDuration: p.config.StorageConfig.SaveHoldMaxDuration,
			SaveHoldMaxCount:    p.config.StorageConfig.SaveHoldMaxCount,
			CacheBackend:        p.config.StorageConfig.CacheBackend,
			RedisCacheConfig:    p.config.StorageConfig.RedisCacheConfig,
			BloomConfig:         p.config.StorageConfig.BloomConfig,
			TraceEsConfig: storage.EsOptions{
				Host:      traceEsConfig.Host,
				Username:  traceEsConfig.Username,
				Password:  traceEsConfig.Password,
				IndexName: traceEsConfig.IndexName,
			},
			SaveEsConfig: storage.EsOptions{
				Host:      saveEsConfig.Host,
				Username:  saveEsConfig.Username,
				Password:  saveEsConfig.Password,
				IndexName: saveEsConfig.IndexName,
			},
			PrometheusWriterConfig: p.config.StorageConfig.PrometheusWriterConfig,
			MetricsConfig:          p.config.StorageConfig.MetricsConfig,
		},
	)
	if err != nil {
		apmLogger.Errorf("Storage fail to started, the calculated data not be saved. error: %s", err)
		return nil, err
	}

	proxy.Run(p.errorReceiveChan)
	p.proxy = proxy
	return proxy.SaveRequest(), nil
}

func (p *RunInstance) startRuntimeCollector() {
	p.RuntimeCollector = NewProfileCollector(p.ctx, p.config.SidecarConfig, p.startInfo.DataId)

	if !p.config.SidecarConfig.EnabledProfile {
		apmLogger.Infof("[!] profileConfig is not configured, the profile will not be reported")
	} else {
		p.RuntimeCollector.StartReport()
	}

	go p.startRecordSemaphoreAcquired()
}

func (p *RunInstance) startRecordSemaphoreAcquired() {

	ticker := time.NewTicker(p.RuntimeCollector.config.MetricsReportInterval)
	apmLogger.Infof(
		"[RecordSemaphoreAcquired] start report chan metric every %s",
		p.RuntimeCollector.config.MetricsReportInterval,
	)
	for {
		select {
		case <-ticker.C:
			metrics.RecordApmPreCalcSemaphoreTotal(p.startInfo.DataId, metrics.TaskProcessChan, len(p.notifier.Spans()))
			metrics.RecordApmPreCalcSemaphoreTotal(
				p.startInfo.DataId, metrics.WindowProcessEventChan, p.windowHandler.Operator.GetWindowsLength(),
			)
			metrics.RecordApmPreCalcSemaphoreTotal(p.startInfo.DataId, metrics.SaveRequestChan, len(p.proxy.SaveRequest()))
			p.windowHandler.Operator.RecordTraceAndSpanCountMetric()

		case <-p.ctx.Done():
			apmLogger.Infof("[RecordSemaphoreAcquired] receive context done, stopped")
			ticker.Stop()
			return
		}
	}
}

// watchConsulConfigUpdate if the config of dataId in consul is updated, will be reload daemon task.
func (p *RunInstance) watchConsulConfigUpdate(errorReceiveChan chan<- error) {
	ticker := time.NewTicker(10 * time.Minute)

	for {
		select {
		case <-ticker.C:
			isUpdated, diff := core.GetMetadataCenter().CheckUpdate(p.startInfo.DataId)
			if isUpdated {
				apmLogger.Infof("[ConsulConfigWatcher] dataId: %s config updated(diff: %s), will be reload!", p.startInfo.DataId, diff)
				errorReceiveChan <- errors.New("reload for config update")
				return
			}
		case <-p.ctx.Done():
			apmLogger.Infof("[ConsulConfigWatcher] dataId: %s consul config update checker exit", p.startInfo.DataId)
			ticker.Stop()
			return
		}
	}

}
