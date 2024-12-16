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
	"sync"
	"time"

	"github.com/imdario/mergo"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
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
	WithMetricReport(MetricOptions) Builder
	Build() PreCalculateProcessor
}

type PreCalculateProcessor interface {
	PreCalculateProcessorStandLone

	Start(stopParentContext context.Context, errorReceiveChan chan<- error, payload []byte)
	GetTaskDimension(payload []byte) string
	Run(errorChan chan<- error)

	StartByDataId(ctx context.Context, startInfo StartInfo, errorReceiveChan chan<- error, config ...PrecalculateOption)
}

var (
	preCalculateOnce     sync.Once
	preCalculateInstance *Precalculate
)

type StartInfo struct {
	DataId string `json:"data_id"`
	Qps    *int   `json:"qps"`
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
	distributiveWindowConfig window.DistributiveWindowOptions
	runtimeConfig            window.RuntimeConfig
	notifierConfig           notifier.Options
	processorConfig          window.ProcessorOptions
	storageConfig            storage.ProxyOptions

	profileReportConfig MetricOptions
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
	p.defaultConfig.notifierConfig = options
	return p
}

func (p *Precalculate) WithWindowRuntimeConfig(options window.RuntimeConfig) Builder {
	p.defaultConfig.runtimeConfig = options
	return p
}

func (p *Precalculate) WithDistributiveWindowConfig(options window.DistributiveWindowOptions) Builder {
	p.defaultConfig.distributiveWindowConfig = options
	return p
}

func (p *Precalculate) WithProcessorConfig(options window.ProcessorOptions) Builder {
	p.defaultConfig.processorConfig = options
	return p
}

func (p *Precalculate) WithStorageConfig(options storage.ProxyOptions) Builder {
	p.defaultConfig.storageConfig = options
	return p
}

func (p *Precalculate) WithMetricReport(options MetricOptions) Builder {
	p.defaultConfig.profileReportConfig = options
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

func (p *Precalculate) StartByDataId(runInstanceCtx context.Context, startInfo StartInfo, errorReceiveChan chan<- error, config ...PrecalculateOption) {
	ticker := time.NewTicker(5 * time.Second)
loop:
	for {
		select {
		case <-ticker.C:
			if err := core.GetMetadataCenter().AddDataId(startInfo.DataId); err != nil {
				apmLogger.Errorf(
					"Failed to start the pre-calculation with dataId: %s, it will not be executed. error: %s",
					startInfo.DataId, err,
				)
				continue
			}

			var signal readySignal
			if len(config) == 0 {
				signal = readySignal{
					ctx: runInstanceCtx, startInfo: startInfo, config: p.defaultConfig, errorReceiveChan: errorReceiveChan,
				}
			} else {
				// config merge
				mergeConfig, err := p.MergeConfig(p.defaultConfig, config[0])
				if err != nil {
					errorReceiveChan <- err
					return
				}
				signal = readySignal{
					ctx: runInstanceCtx, startInfo: startInfo, config: mergeConfig, errorReceiveChan: errorReceiveChan,
				}
			}
			p.readySignalChan <- signal
			break loop
		case <-runInstanceCtx.Done():
			logger.Infof("StartByDataId stopped.")
			ticker.Stop()
			break loop
		}
	}

	apmLogger.Infof("[StartByDataId] done - DataId: %s Qps: %d", startInfo.DataId, startInfo.Qps)
}

func (p *Precalculate) MergeConfig(defaultConfig, customConfig PrecalculateOption) (PrecalculateOption, error) {
	if err := mergo.Merge(&defaultConfig, customConfig); err != nil {
		return PrecalculateOption{}, fmt.Errorf("[Precalculate] failed to merge config, error: %s", err)
	}
	return defaultConfig, nil
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

	RuntimeCollector ProfileCollector
}

func (p *RunInstance) startNotifier() (<-chan []window.StandardSpan, error) {
	kafkaConfig := core.GetMetadataCenter().GetKafkaConfig(p.startInfo.DataId)
	groupId := "go-apm-pre-calculate-consumer-group"
	var qps int
	if p.startInfo.Qps == nil {
		qps = config.NotifierMessageQps
	} else {
		qps = *p.startInfo.Qps
	}

	n, err := notifier.NewNotifier(
		notifier.KafkaNotifier,
		p.startInfo.DataId,
		notifier.Options{
			ChanBufferSize: p.config.notifierConfig.ChanBufferSize,
			Ctx:            p.ctx,
			KafkaConfig: notifier.KafkaConfig{
				KafkaGroupId:  groupId,
				KafkaHost:     kafkaConfig.Host,
				KafkaUsername: kafkaConfig.Username,
				KafkaPassword: kafkaConfig.Password,
				KafkaTopic:    kafkaConfig.Topic,
			},
			Qps: qps,
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

	processor := window.NewProcessor(p.ctx, p.startInfo.DataId, p.proxy, p.config.processorConfig)

	operation := window.Operation{
		Operator: window.NewDistributiveWindow(
			p.startInfo.DataId,
			p.ctx,
			processor,
			saveReqChan,
			p.config.distributiveWindowConfig,
		),
	}
	operation.Run(messageChan, p.errorReceiveChan, p.config.runtimeConfig)

	p.windowHandler = operation
}

func (p *RunInstance) startStorageBackend() (chan<- storage.SaveRequest, error) {
	traceEsConfig := core.GetMetadataCenter().GetTraceEsConfig(p.startInfo.DataId)
	saveEsConfig := core.GetMetadataCenter().GetSaveEsConfig(p.startInfo.DataId)

	proxy, err := storage.NewProxyInstance(
		p.startInfo.DataId,
		p.ctx,
		storage.ProxyOptions{
			WorkerCount:      p.config.storageConfig.WorkerCount,
			SaveHoldDuration: p.config.storageConfig.SaveHoldDuration,
			SaveHoldMaxCount: p.config.storageConfig.SaveHoldMaxCount,
			CacheBackend:     p.config.storageConfig.CacheBackend,
			RedisCacheConfig: p.config.storageConfig.RedisCacheConfig,
			BloomConfig:      p.config.storageConfig.BloomConfig,
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
			PrometheusWriterConfig: remote.PrometheusWriterOptions{
				Url:     config.PromRemoteWriteUrl,
				Headers: config.PromRemoteWriteHeaders,
			},
			MetricsConfig: p.config.storageConfig.MetricsConfig,
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
	p.RuntimeCollector = NewProfileCollector(p.ctx, p.config.profileReportConfig, p.startInfo.DataId)

	if !p.config.profileReportConfig.EnabledProfile {
		apmLogger.Infof("[!] profileConfig is not configured, the profile will not be reported")
	} else {
		p.RuntimeCollector.StartReport()
	}

	go p.startRecordSemaphoreAcquired()
}

func (p *RunInstance) startRecordSemaphoreAcquired() {

	ticker := time.NewTicker(p.RuntimeCollector.config.ReportInterval)
	apmLogger.Infof(
		"[RecordSemaphoreAcquired] start report chan metric every %s",
		p.RuntimeCollector.config.ReportInterval,
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
