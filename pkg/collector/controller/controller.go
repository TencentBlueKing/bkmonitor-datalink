// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controller

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/cleaner"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/hook"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/labelstore"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tracestore"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/wait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pingserver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/proxy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pusher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	configFieldMaxProcs     = "max_procs"
	configFieldLogging      = "logging"
	configFieldLabelStorage = "label_storage"
	configFieldTraceStorage = "trace_storage"
	configFieldHook         = "hook"
)

type Controller struct {
	ctx       context.Context
	cancel    context.CancelFunc
	conf      *confengine.Config
	wg        sync.WaitGroup
	buildInfo define.BuildInfo

	pusherMgr     pusher.Pusher
	receiverMgr   *receiver.Receiver
	pipelineMgr   *pipeline.Manager
	exporterMgr   *exporter.Exporter
	proxyMgr      *proxy.Proxy
	pingserverMgr *pingserver.Pingserver
	clusterSvr    *cluster.Server

	originalTasks *define.TaskQueue
	derivedTasks  *define.TaskQueue
}

func SetupCoreNum(conf *confengine.Config) {
	define.SetCoreNum(conf.UnpackIntWithDefault(configFieldMaxProcs, 0))
}

type StorageConfig struct {
	Type string `config:"type" mapstructure:"type"`
	Dir  string `config:"dir" mapstructure:"dir"`
}

// SetupLabelStorage 初始化 Label Storage
func SetupLabelStorage(conf *confengine.Config) {
	var storConf StorageConfig
	if err := conf.UnpackChild(configFieldLabelStorage, &storConf); err != nil {
		logger.Warnf("unpack label storage config failed, may it lacks of fields: %s, then uses the default config", err)
	}
	labelstore.InitStorage(storConf.Dir, storConf.Type)
}

// SetupTraceStorage 初始化 Trace Storage
func SetupTraceStorage(conf *confengine.Config) {
	var storConf StorageConfig
	if err := conf.UnpackChild(configFieldTraceStorage, &storConf); err != nil {
		logger.Warnf("unpack trace storage config failed, may it lacks of fields: %s, then uses the default config", err)
	}
	tracestore.InitStorage(storConf.Dir, storConf.Type)
}

// SetupHook 初始化 Hook
func SetupHook(conf *confengine.Config) {
	var hookConf hook.Config
	if err := conf.UnpackChild(configFieldHook, &hookConf); err != nil {
		logger.Warnf("unpack hook config failed, may it lacks of fields: %s, then uses the default config", err)
	}
	hook.Register(hookConf)
}

// SetupLogger 初始化 Logger
func SetupLogger(conf *confengine.Config) error {
	type LogConfig struct {
		Stdout  bool   `config:"stdout"`
		Level   string `config:"level"`
		Format  string `config:"format"`
		Path    string `config:"path"`
		MaxSize int    `config:"maxsize"`
		MaxAge  int    `config:"maxage"`
		Backups int    `config:"backups"`
	}
	var logCfg LogConfig
	if err := conf.UnpackChild(configFieldLogging, &logCfg); err != nil {
		return err
	}

	logger.SetOptions(logger.Options{
		Stdout:     logCfg.Stdout,
		Format:     logCfg.Format,
		Filename:   filepath.Join(logCfg.Path, "bk-collector.log"),
		MaxSize:    logCfg.MaxSize,
		MaxAge:     logCfg.MaxAge,
		MaxBackups: logCfg.Backups,
		Level:      logCfg.Level,
	})
	return nil
}

func Setup(conf *confengine.Config) error {
	SetupCoreNum(conf)
	if err := SetupLogger(conf); err != nil {
		return err
	}
	SetupLabelStorage(conf)
	SetupTraceStorage(conf)
	SetupHook(conf)
	return nil
}

func New(conf *confengine.Config, buildInfo define.BuildInfo) (*Controller, error) {
	var err error
	if err = Setup(conf); err != nil {
		return nil, err
	}

	var receiverMgr *receiver.Receiver
	if !conf.Disabled(define.ConfigFieldReceiver) {
		receiverMgr, err = receiver.New(conf)
		if err != nil {
			return nil, err
		}
	}

	var exporterMgr *exporter.Exporter
	if !conf.Disabled(define.ConfigFieldExporter) {
		exporterMgr, err = exporter.New(conf)
		if err != nil {
			return nil, err
		}
	}

	var proxyMgr *proxy.Proxy
	if !conf.Disabled(define.ConfigFieldProxy) {
		proxyMgr, err = proxy.New(conf)
		if err != nil {
			return nil, err
		}
	}

	var pingserverMgr *pingserver.Pingserver
	if !conf.Disabled(define.ConfigFieldPingserver) {
		pingserverMgr, err = pingserver.New(conf)
		if err != nil {
			return nil, err
		}
	}

	var clusterSvr *cluster.Server
	if !conf.Disabled(define.ConfigFieldCluster) {
		clusterSvr, err = cluster.NewServer(conf)
		if err != nil {
			return nil, err
		}
	}

	pipelineMgr, err := pipeline.New(conf)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	var pusherMgr pusher.Pusher
	if !conf.Disabled(define.ConfigFieldPusher) {
		pusherMgr, err = pusher.New(ctx, conf)
		if err != nil {
			cancel()
			return nil, err
		}
	}

	// 注册 gse output hook 统计发送数据
	gse.RegisterSendHook(DefaultMetricMonitor.ObserveBeatSentBytes)

	return &Controller{
		ctx:           ctx,
		cancel:        cancel,
		conf:          conf,
		buildInfo:     buildInfo,
		pusherMgr:     pusherMgr,
		receiverMgr:   receiverMgr,
		proxyMgr:      proxyMgr,
		pingserverMgr: pingserverMgr,
		clusterSvr:    clusterSvr,
		exporterMgr:   exporterMgr,
		pipelineMgr:   pipelineMgr,
		originalTasks: define.NewTaskQueue(define.PushModeGuarantee),
		derivedTasks:  define.NewTaskQueue(define.PushModeGuarantee),
	}, nil
}

func (c *Controller) recordMetrics() {
	c.wg.Add(1)
	defer c.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			DefaultMetricMonitor.UpdateUptime(5)
			DefaultMetricMonitor.SetAppBuildInfo(c.buildInfo)

		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Controller) Start() error {
	go c.recordMetrics()
	for i := 0; i < define.Concurrency(); i++ {
		go wait.Until(c.ctx, c.consumeRecords)
		go wait.Until(c.ctx, c.consumeNonSchedRecords)
		go wait.Until(c.ctx, c.dispatchOriginalTasks)
		go wait.Until(c.ctx, c.dispatchDerivedTasks)
	}

	if c.receiverMgr != nil {
		if err := c.receiverMgr.Start(); err != nil {
			return err
		}
	}

	if c.proxyMgr != nil {
		if err := c.proxyMgr.Start(); err != nil {
			return err
		}
	}

	if c.pingserverMgr != nil {
		if err := c.pingserverMgr.Start(); err != nil {
			return err
		}
	}

	if c.exporterMgr != nil {
		if err := c.exporterMgr.Start(); err != nil {
			return err
		}
	}

	if c.pusherMgr != nil {
		if err := c.pusherMgr.Start(); err != nil {
			return err
		}
	}

	if c.clusterSvr != nil {
		if err := c.clusterSvr.Start(); err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) Stop() error {
	if c.receiverMgr != nil {
		if err := c.receiverMgr.Stop(); err != nil {
			return err
		}
	}

	if c.proxyMgr != nil {
		if err := c.proxyMgr.Stop(); err != nil {
			return err
		}
	}

	if c.pingserverMgr != nil {
		c.pingserverMgr.Stop()
	}

	if c.exporterMgr != nil {
		c.exporterMgr.Stop()
	}

	if c.pusherMgr != nil {
		c.pusherMgr.Stop()
	}

	if c.clusterSvr != nil {
		c.clusterSvr.Stop()
	}

	cleanFuncs := cleaner.CleanFuncs()
	for name, fn := range cleanFuncs {
		if err := fn(); err != nil {
			logger.Errorf("failed to execute clean function, name=%s, err: %v", name, err)
		}
	}

	c.cancel()
	c.wg.Wait()
	logger.Info("controller has already stopped")
	return nil
}

func (c *Controller) Reload(conf *confengine.Config) error {
	t0 := time.Now()
	logger.Info("reloading controller")

	if err := c.pipelineMgr.Reload(conf); err != nil {
		DefaultMetricMonitor.IncReloadFailedCounter()
		logger.Errorf("failed to reload pipeline manager: %v", err)
		return err
	}

	if c.pingserverMgr != nil {
		if err := c.pingserverMgr.Reload(conf); err != nil {
			DefaultMetricMonitor.IncReloadFailedCounter()
			logger.Errorf("failed to reload pingserver manager: %v", err)
			return err
		}
	}

	if c.exporterMgr != nil {
		c.exporterMgr.Reload(conf)
	}

	since := time.Since(t0)
	logger.Infof("reload finished, take: %v", since)
	DefaultMetricMonitor.IncReloadSuccessCounter()
	DefaultMetricMonitor.ObserveReloadDuration(t0)
	return nil
}

func (c *Controller) submitTasks(q *define.TaskQueue, record *define.Record, pipeline pipeline.Pipeline) {
	if pipeline == nil {
		logger.Warnf("no '%s' pipeline found", record.RecordType)
		return
	}
	q.Push(define.NewTask(record, pipeline.Name(), pipeline.SchedProcessors()))
}

// consumeNonSchedRecords 消费来自 accumulator 提交的数据
func (c *Controller) consumeNonSchedRecords() {
	c.wg.Add(1)
	defer c.wg.Done()

	for {
		select {
		case record, ok := <-processor.NonSchedRecords():
			if !ok {
				return
			}
			exporter.PublishRecord(record)

		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Controller) consumeRecords() {
	c.wg.Add(1)
	defer c.wg.Done()

	for {
		select {
		case record, ok := <-receiver.Records():
			if !ok {
				return
			}
			pl := c.pipelineMgr.GetPipeline(record.RecordType)
			c.submitTasks(c.originalTasks, record, pl)

		case record, ok := <-proxy.Records():
			if !ok {
				return
			}
			pl := c.pipelineMgr.GetPipeline(record.RecordType)
			c.submitTasks(c.originalTasks, record, pl)

		case record, ok := <-pingserver.Records():
			if !ok {
				return
			}
			pl := c.pipelineMgr.GetPipeline(record.RecordType)
			c.submitTasks(c.originalTasks, record, pl)

		case record, ok := <-cluster.Records():
			if !ok {
				return
			}
			pl := c.pipelineMgr.GetPipeline(record.RecordType)
			record.Unwrap()
			c.submitTasks(c.originalTasks, record, pl)

		case <-c.ctx.Done():
			return
		}
	}
}

// dispatchOriginalTasks 分发原始任务
func (c *Controller) dispatchOriginalTasks() {
	c.wg.Add(1)
	defer c.wg.Done()

loop:
	for {
		select {
		case task, ok := <-c.originalTasks.Get():
			if !ok {
				return
			}

			start := time.Now()
			rtype := task.Record().RecordType
			for i := 0; i < task.StageCount(); i++ {
				// 任务执行应该事务的 一旦中间某一环执行失败那就整体失败
				stage := task.StageAt(i)
				logger.Debugf("process original stage: %s, recordType: %+v", stage, task.Record().RecordType)
				derivedRecord, err := c.pipelineMgr.GetProcessor(stage).Process(task.Record())
				if err == define.ErrSkipEmptyRecord {
					token := task.Record().Token
					DefaultMetricMonitor.IncSkippedCounter(task.PipelineName(), rtype, token.GetDataID(rtype), stage, token.Original)
					logger.Warnf("skip empty record '%s' at stage: %v, token: %+v, err: %v", task.Record().RecordType, stage, token, err)
					goto loop
				}
				if err == define.ErrEndOfPipeline {
					goto loop
				}

				if err != nil {
					logger.Errorf("failed to process task: %v", err)
					DefaultMetricMonitor.IncDroppedCounter(task.PipelineName(), rtype, task.Record().Token.GetDataID(rtype), stage)
					goto loop
				}

				if derivedRecord != nil {
					pl := c.pipelineMgr.GetPipeline(derivedRecord.RecordType)
					derivedRecord.Unwrap()
					c.submitTasks(c.derivedTasks, derivedRecord, pl)
				}
			}

			token := task.Record().Token
			DefaultMetricMonitor.ObserveHandledDuration(start, task.PipelineName(), rtype, token.GetDataID(rtype))

			t0 := time.Now()
			exporter.PublishRecord(task.Record())
			logger.Debugf("original handle record: %+v, token: %+v", task.Record().RecordType, token)

			// no processors
			if task.StageCount() == 0 {
				continue
			}
			DefaultMetricMonitor.ObserveExportedDuration(t0, task.PipelineName(), rtype, token.GetDataID(rtype))
			DefaultMetricMonitor.IncHandledCounter(task.PipelineName(), rtype, token.GetDataID(rtype), token.Original)

		case <-c.ctx.Done():
			return
		}
	}
}

// dispatchDerivedTasks 分发派生任务
func (c *Controller) dispatchDerivedTasks() {
	c.wg.Add(1)
	defer c.wg.Done()

loop:
	for {
		select {
		case task, ok := <-c.derivedTasks.Get():
			if !ok {
				return
			}

			start := time.Now()
			rtype := task.Record().RecordType
			for i := 0; i < task.StageCount(); i++ {
				// 任务执行应该事务的 一旦中间某一环执行失败那就整体失败
				// 无需再关注是否为 derived 类型
				stage := task.StageAt(i)
				logger.Debugf("process derived stage: %s, recordType: %+v", stage, task.Record().RecordType)
				_, err := c.pipelineMgr.GetProcessor(stage).Process(task.Record())
				if err == define.ErrSkipEmptyRecord {
					token := task.Record().Token
					logger.Warnf("skip empty record '%s' at stage: %v, token: %+v, err: %v", task.Record().RecordType, stage, token, err)
					DefaultMetricMonitor.IncSkippedCounter(task.PipelineName(), rtype, token.GetDataID(rtype), stage, token.Original)
					goto loop
				}
				if err == define.ErrEndOfPipeline {
					goto loop
				}

				if err != nil {
					logger.Errorf("failed to process task: %v", err)
					DefaultMetricMonitor.IncDroppedCounter(task.PipelineName(), rtype, task.Record().Token.GetDataID(rtype), stage)
					goto loop
				}
			}

			token := task.Record().Token
			DefaultMetricMonitor.ObserveHandledDuration(start, task.PipelineName(), rtype, token.GetDataID(rtype))

			t0 := time.Now()
			exporter.PublishRecord(task.Record())
			logger.Debugf("derived handle record: %+v, token: %+v", task.Record().RecordType, token)

			// no processors
			if task.StageCount() == 0 {
				continue
			}
			DefaultMetricMonitor.ObserveExportedDuration(t0, task.PipelineName(), rtype, token.GetDataID(rtype))
			DefaultMetricMonitor.IncHandledCounter(task.PipelineName(), rtype, token.GetDataID(rtype), token.Original)

		case <-c.ctx.Done():
			return
		}
	}
}
