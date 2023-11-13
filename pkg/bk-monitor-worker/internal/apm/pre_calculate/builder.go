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

	jsoniter "github.com/json-iterator/go"
	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Builder Pre-Calculate default configuration builder
type Builder interface {
	WithContext(context.Context, context.CancelFunc) Builder
	WithNotifierConfig(options ...notifier.Option) Builder
	WithWindowRuntimeConfig(...window.RuntimeConfigOption) Builder
	WithDistributiveWindowConfig(options ...window.DistributiveWindowOption) Builder
	WithProcessorConfig(options ...window.ProcessorOption) Builder
	WithStorageConfig(options ...storage.ProxyOption) Builder
	WithMetricReport(options ...MetricOption) Builder
	Build() PreCalculateProcessor
}

type PreCalculateProcessor interface {
	Start(stopParentContext context.Context, errorReceiveChan chan<- error, payload []byte)
	Run()
	Stop(dataId string)

	StartByDataId(ctx context.Context, dataId string, errorReceiveChan chan<- error, config ...PrecalculateOption)

	WatchConnections(filePath string)
}

var (
	preCalculateOnce     sync.Once
	preCalculateInstance *Precalculate
)

type StartInfo struct {
	DataId string `json:"data_id"`
}

type Precalculate struct {
	ctx    context.Context
	cancel context.CancelFunc

	// defaultConfig is the global default configuration for pre-calculate.
	// If a dataId needs to be configured independently, you can override it using config in the Start method
	defaultConfig PrecalculateOption

	readySignalChan  chan readySignal
	runningInstances []*RunInstance

	httpTransport *http.Transport
}

type PrecalculateOption struct {
	// window-specific-config
	distributiveWindowConfig []window.DistributiveWindowOption
	runtimeConfig            []window.RuntimeConfigOption
	notifierConfig           []notifier.Option
	processorConfig          []window.ProcessorOption
	storageConfig            []storage.ProxyOption

	metricReportConfig []MetricOption
}

type readySignal struct {
	ctx              context.Context
	dataId           string
	config           PrecalculateOption
	errorReceiveChan chan<- error
}

func (p *Precalculate) WithContext(ctx context.Context, cancel context.CancelFunc) Builder {
	p.ctx = ctx
	p.cancel = cancel
	return p
}

func (p *Precalculate) WithNotifierConfig(options ...notifier.Option) Builder {
	p.defaultConfig.notifierConfig = options
	return p
}

func (p *Precalculate) WithWindowRuntimeConfig(options ...window.RuntimeConfigOption) Builder {
	p.defaultConfig.runtimeConfig = options
	return p
}

func (p *Precalculate) WithDistributiveWindowConfig(options ...window.DistributiveWindowOption) Builder {
	p.defaultConfig.distributiveWindowConfig = options
	return p
}

func (p *Precalculate) WithProcessorConfig(options ...window.ProcessorOption) Builder {
	p.defaultConfig.processorConfig = options
	return p
}

func (p *Precalculate) WithStorageConfig(options ...storage.ProxyOption) Builder {
	p.defaultConfig.storageConfig = options
	return p
}

func (p *Precalculate) WithMetricReport(options ...MetricOption) Builder {
	p.defaultConfig.metricReportConfig = options
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

	return &Precalculate{readySignalChan: make(chan readySignal, 0), httpTransport: httpMetricTransport}
}

func (p *Precalculate) Start(ctx context.Context, errorReceiveChan chan<- error, payload []byte) {

	var startInfo StartInfo
	if err := jsoniter.Unmarshal(payload, &startInfo); err != nil {
		logger.Errorf(
			"Failed to start APM-Precalculate as parse value to StartInfo error, value: %s. error: %s",
			payload, err,
		)
		return
	}

	p.StartByDataId(ctx, startInfo.DataId, errorReceiveChan)
}

func (p *Precalculate) StartByDataId(ctx context.Context, dataId string, errorReceiveChan chan<- error, config ...PrecalculateOption) {
	retryCount := 0
	ticker := time.NewTicker(time.Second)
loop:
	for {
		select {
		case <-ticker.C:
			if err := core.GetMetadataCenter().AddDataId(dataId); err != nil {
				retryCount++
				if retryCount > 10 {
					errorReceiveChan <- fmt.Errorf(
						"failed to start the pre-calculation "+
							"with dataId: %s after 10 retries, giving up. error: %s", dataId, err,
					)
					break loop
				}
				apmLogger.Errorf(
					"Failed to start the pre-calculation with dataId: %s, it will not be executed. error: %s",
					dataId, err,
				)
				ticker = time.NewTicker(time.Duration(retryCount*5) * time.Second)
			} else {
				var signal readySignal
				if len(config) == 0 {
					signal = readySignal{ctx: ctx, dataId: dataId, config: p.defaultConfig, errorReceiveChan: errorReceiveChan}
				} else {
					signal = readySignal{ctx: ctx, dataId: dataId, config: config[0], errorReceiveChan: errorReceiveChan}
				}
				p.readySignalChan <- signal
				break loop
			}
		case <-ctx.Done():
			logger.Infof("StartByDataId stopped.")
			break loop
		}
	}
}

func (p *Precalculate) Run() {
	core.CreateMetadataCenter()
	apmLogger.Infof("Pre-calculate is running...")
loop:
	for {
		select {
		case signal := <-p.readySignalChan:
			apmLogger.Infof("Pre-calculation with dataId: %s was received.", signal.dataId)
			p.launch(signal.ctx, signal.dataId, signal.config, signal.errorReceiveChan)
		case <-p.ctx.Done():
			apmLogger.Info("Precalculate[MAIN] received the stop signal.")
			break loop
		}
	}
}

func (p *Precalculate) Stop(dataId string) {
	p.runningInstances = slices.DeleteFunc(p.runningInstances, func(e *RunInstance) bool {
		if e.dataId == dataId {
			e.cancel()
			apmLogger.Infof("dataId: %s stopped.", dataId)
			return true
		}
		return false
	})
}

func (p *Precalculate) launch(parentCtx context.Context, dataId string, conf PrecalculateOption, errorReceiveChan chan<- error) {
	ctx, cancel := context.WithCancel(parentCtx)
	runInstance := RunInstance{dataId: dataId, config: conf, ctx: ctx, cancel: cancel, errorReceiveChan: errorReceiveChan}

	messageChan := runInstance.startNotifier()
	saveReqChan := runInstance.startStorageBackend()
	runInstance.startWindowHandler(messageChan, saveReqChan, errorReceiveChan)

	runInstance.startMetricReport(p.httpTransport)

	apmLogger.Infof("dataId: %s launch successfully", dataId)
	p.runningInstances = append(p.runningInstances, &runInstance)
}

type RunInstance struct {
	dataId           string
	config           PrecalculateOption
	errorReceiveChan chan<- error

	ctx    context.Context
	cancel context.CancelFunc

	notifier      notifier.Notifier
	windowHandler window.Operator
	proxy         *storage.Proxy

	metricCollector MetricCollector
}

func (p *RunInstance) startNotifier() <-chan []window.StandardSpan {
	kafkaConfig := core.GetMetadataCenter().GetKafkaConfig(p.dataId)
	groupId := "go-pre-calculate-worker-consumer"
	n := notifier.NewNotifier(
		notifier.KafkaNotifier,
		append([]notifier.Option{
			notifier.Context(p.ctx),
			notifier.KafkaGroupId(groupId),
			notifier.KafkaHost(kafkaConfig.Host),
			notifier.KafkaUsername(kafkaConfig.Username),
			notifier.KafkaPassword(kafkaConfig.Password),
			notifier.KafkaTopic(kafkaConfig.Topic),
		}, p.config.notifierConfig...,
		)...,
	)
	p.notifier = n
	go n.Start(p.errorReceiveChan)
	return n.Spans()
}

func (p *RunInstance) startWindowHandler(messageChan <-chan []window.StandardSpan, saveReqChan chan<- storage.SaveRequest, errorReceiveChan chan<- error) {

	processor := window.NewProcessor(p.dataId, p.proxy, p.config.processorConfig...)

	operator := window.NewDistributiveWindow(p.dataId, p.ctx, processor, saveReqChan, p.config.distributiveWindowConfig...)
	operation := window.Operation{Operator: operator}
	operation.Run(messageChan, errorReceiveChan, p.config.runtimeConfig...)

	p.windowHandler = operator
}

func (p *RunInstance) startStorageBackend() chan<- storage.SaveRequest {
	traceEsConfig := core.GetMetadataCenter().GetTraceEsConfig(p.dataId)
	saveEsConfig := core.GetMetadataCenter().GetSaveEsConfig(p.dataId)

	proxy, err := storage.NewProxyInstance(
		p.ctx,
		append([]storage.ProxyOption{
			storage.TraceEsConfig(
				storage.EsHost(traceEsConfig.Host),
				storage.EsUsername(traceEsConfig.Username),
				storage.EsPassword(traceEsConfig.Password),
				storage.EsIndexName(traceEsConfig.IndexName),
			),
			storage.SaveEsConfig(
				storage.EsHost(saveEsConfig.Host),
				storage.EsUsername(saveEsConfig.Username),
				storage.EsPassword(saveEsConfig.Password),
				storage.EsIndexName(saveEsConfig.IndexName),
			),
		}, p.config.storageConfig...)...,
	)
	if err != nil {
		apmLogger.Errorf("Storage fail to started, the calculated data may not be saved. error: %s", err)
		return nil
	}

	proxy.Run(p.errorReceiveChan)
	p.proxy = proxy
	return proxy.SaveRequest()
}

func (p *RunInstance) startMetricReport(transport *http.Transport) {
	if len(p.config.metricReportConfig) == 0 {
		apmLogger.Infof("[!] metric is not configured, the indicator will not be reported")
		return
	}

	opt := MetricOptions{}
	for _, setter := range p.config.metricReportConfig {
		setter(&opt)
	}

	p.metricCollector = NewMetricCollector(opt, transport, p)
	p.metricCollector.StartReport()
}
