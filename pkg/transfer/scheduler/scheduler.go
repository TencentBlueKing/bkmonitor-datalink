// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// Scheduler :
type Scheduler struct {
	Name            string
	TaskManager     *define.TaskManager
	PipelineManager *PipelineManager

	ctx            context.Context
	cancelFn       context.CancelFunc
	waitGroup      sync.WaitGroup
	cleanUpTimeout time.Duration
	watcher        Watcher
	store          define.Store
	// scheduler在创建cmdb数据同步任务时，需要判断当前service是否为leader
	service      define.Service
	consulClient consul.ClientAPI
	flowPath     string
}

// CheckPipelineConfig :
func (s *Scheduler) CheckPipelineConfig(pipe *config.PipelineConfig) error {
	if pipe.ETLConfig == "" {
		return fmt.Errorf("etl config is empty")
	}
	return pipe.Clean()
}

// 启动cc cache 和 http server
func (s *Scheduler) buildPlugin(ctx context.Context, conf define.Configuration) error {
	if conf.GetBool(ConfSchedulerPluginHTTPServer) {
		s.TaskManager.Add(http.NewServer(ctx, conf))
	}
	if conf.GetBool(ConfSchedulerPluginCCCache) {
		if err := s.addCCUpdateTask(ctx, conf); err != nil {
			return err
		}
	}
	return nil
}

// addCCUpdateTask: 判断是否要启动ccUpdate, 或者哪一种update
func (s *Scheduler) addCCUpdateTask(ctx context.Context, conf define.Configuration) error {
	storageType := conf.GetString(storage.ConfStorageType)
	// 判断缓存的维护模式是否为只需leader维护
	mode := storage.StoreMode[storageType]
	// 此处判断是否开启cc缓存
	val := ctx.Value(define.ContextStartCacheKey)
	isStartCache, ok := val.(bool)
	// 获取失败，则默认不开启cc同步
	if !ok {
		isStartCache = false
	}
	if !isStartCache {
		logging.Warnf("not add ccUpdateTask")
		return nil
	}
	logging.Infof("add ccUpdateTask")
	if s.service != nil && ok && mode == storage.OnlyLeader {
		ch := make(chan context.CancelFunc, 1)
		bus := s.service.EventBus()
		err := bus.SubscribeAsync(consul.EvPromoted, func(id string) {
			logging.Infof("the storage type is [%s] and is maintained only by the leader->[%s]", storageType, id)
			subCtx, cancel := context.WithCancel(ctx)
			select {
			case ch <- cancel:
				go func() {
					logging.Infof("NewCCHostUpdateTask Start")
					err := NewCCHostUpdateTask(subCtx, conf).Start()
					if err != nil {
						panic(err)
					}
				}()
			default:
				cancel()
			}
		}, false)
		if err != nil {
			return err
		}
		// 订阅leader退休事件
		err = bus.Subscribe(consul.EvRetired, func(id string) {
			cancel := <-ch
			cancel()
		})
		if err != nil {
			return err
		}

	} else {
		logging.Infof("the storage type is [%s] and is maintained only by every service", storageType)
		s.TaskManager.Add(NewCCHostUpdateTask(ctx, conf))
	}
	return nil
}

func (s *Scheduler) build(ctx context.Context) error {
	var err error
	conf := config.FromContext(ctx)
	ctx = IntoContext(ctx, s)

	storeType := conf.GetString(storage.ConfStorageType)
	s.store, err = define.NewStore(ctx, storeType)
	if err != nil {
		return errors.Wrapf(err, "create store %s filed", storeType)
	}
	ctx = define.StoreIntoContext(ctx, s.store)
	define.ExposeStore(s.store, storeType)
	ctx, cancelFn := context.WithCancel(ctx)
	s.ctx = ctx
	s.cancelFn = cancelFn

	return s.buildPlugin(ctx, conf)
}

func (s *Scheduler) handleWatchEvent(ev *define.WatchEvent) {
	var err error
	defer utils.RecoverError(func(e error) {
		MonitorPipelinePanic.Inc()
		logging.Errorf("pending pipeline panic: %+v", errors.WithStack(e))
	})

	conf, ok := ev.Data.(*config.PipelineConfig)
	if !ok {
		logging.Errorf("unknown event received %+v", ev)
		return
	}

	err = s.CheckPipelineConfig(conf)
	if err != nil {
		logging.Infof("skip event %#v because of %v", conf, err)
		return
	}

	etlConfig := conf.ETLConfig
	dataID := conf.DataID
	pipeline.SetPipelineMeta(conf.DataID, conf.TypeLabel, conf.ETLConfig)

	logging.Debugf("scheduler received %v event of data id %v: %+v", ev.Type, dataID, conf)
	manager := s.PipelineManager

	// 监听自己实例下面的 dataid 的变化情况 即自己的工作内容
	switch ev.Type {
	case define.WatchEventAdded:
		logging.Infof("activate pipeline %d(%s)", dataID, etlConfig)
		err = manager.Activate(s.ctx, conf)
		if err != nil {
			logging.Errorf("activate pipeline %d(%s) failed: %v", dataID, etlConfig, err)
		} else {
			logging.Infof("activate pipeline %d(%s) finished", dataID, etlConfig)
		}
	case define.WatchEventDeleted:
		logging.Infof("deactivate pipeline %d(%s)", dataID, etlConfig)
		err = manager.Deactivate(dataID)
		if err != nil {
			logging.Errorf("deactivate pipeline %d(%s) failed: %v", dataID, etlConfig, err)
		} else {
			logging.Infof("deactivate pipeline %d(%s) finished", dataID, etlConfig)
		}
	case define.WatchEventModified:
		if !manager.IsConfigChanged(conf) {
			logging.Debugf("pipeline %d(%s) up to date, skipped", dataID, etlConfig)
			break
		}

		logging.Infof("reactivate pipeline %d(%s)", dataID, etlConfig)
		err = manager.Reactivate(s.ctx, conf)
		if err != nil {
			logging.Errorf("deactivate pipeline %d(%s) failed: %v", dataID, etlConfig, err)
		} else {
			logging.Infof("reactivate pipeline %d(%s) finished", dataID, etlConfig)
		}
	case define.WatchEventNoChange:
	default:
		logging.Warnf("unknown event type %v", ev.Type)
	}
}

func (s *Scheduler) pendingPipeline(message string, fn func()) {
	MonitorPendingPipeline.Add(1)
	s.waitGroup.Add(1)
	conf := config.FromContext(s.ctx)
	timeout := conf.GetDuration(ConfSchedulerPendingTimeoutKey)
	go func() {
		defer MonitorPendingPipeline.Sub(1)
		logging.Infof("pending pipeline: %s", message)

		err := utils.WaitOrTimeOut(timeout, fn)
		if err != nil {
			logging.Fatalf("wait pending pipeline failed, %s, error %+v", message, errors.WithStack(err))
		}
		s.waitGroup.Done()
	}()
}

func (s *Scheduler) handleKillChannels() {
	var err error
	defer utils.RecoverError(func(e error) {
		logging.Fatalf("%v handle pipeline kill channels panic %+v", s, e)
		MonitorPipelinePanic.Inc()
	})

	deadPipelines := make(map[int]*PipelineItem)
	err = s.PipelineManager.EachItem(func(dataID int, item *PipelineItem) error {
	loop:
		for {
			select {
			case err, open := <-item.KillChan:
				deadPipelines[dataID] = item
				logging.Errorf("killing pipeline[%v] %v by error: %+v", dataID, item.Pipeline, err)
				// channel 关闭 同样需要退出
				if !open {
					break
				}
			default:
				break loop
			}
		}
		return nil
	})
	if err != nil {
		logging.Errorf("check kill channel error %v", err)
	}

	manager := s.PipelineManager
	for i, p := range deadPipelines {
		s.pendingPipeline(fmt.Sprintf("reactivate pipeline %v", p.Config.DataID), func(id int, item *PipelineItem) func() {
			return func() {
				logging.Infof("killing pipeline %d:%v", id, item.Pipeline)

				duration := s.cleanUpTimeout
				err := manager.Deactivate(id)
				if err != nil {
					logging.Errorf("kill pipeline %d:%v failed: %+v", id, item.Pipeline, err)
				}

				logging.Infof("pipeline %d:%v killed, restart after %v", id, item.Pipeline, duration)
				for {
					_, ok := utils.TimeoutOrContextDone(s.ctx, time.After(duration))
					if ok {
						logging.Infof("abort pipeline %d:%v because of context done", id, item.Pipeline)
						break
					}

					err := manager.Activate(s.ctx, item.Config)
					if err == nil {
						logging.Infof("pipeline %d:%v recovered", id, item.Pipeline)
						break
					}
					logging.Errorf("recover pipeline %d:%v failed: %+v, retry after %v", id, item.Pipeline, err, duration)
				}
			}
		}(i, p))
	}
}

func (s *Scheduler) recordFlow(dataid, flow int) error {
	api := s.consulClient.KV()
	kvpair := &consul.KVPair{
		Key:   utils.ResolveUnixPath(s.flowPath, strconv.Itoa(dataid)),
		Value: []byte(strconv.Itoa(flow)),
	}

	ctx, cancel := context.WithTimeout(s.ctx, define.DefaultConsulTimeout)
	defer cancel()

	_, err := api.Put(kvpair, consul.NewWriteOptions(ctx))
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Start :
func (s *Scheduler) Start() error {
	var err error
	conf := config.FromContext(s.ctx)
	ticker := time.NewTicker(conf.GetDuration(ConfSchedulerCheckIntervalKey))
	s.waitGroup.Add(1)
	defer s.waitGroup.Done()

	logging.Infof("scheduler %s ready", s.Name)

	err = s.TaskManager.Start()
	if err != nil {
		return err
	}

	root := utils.ResolveUnixPath(conf.GetString(consul.ConfKeyServicePath), "flow")
	id := fmt.Sprintf("%s-%s", conf.GetString(consul.ConfKeyServiceName), define.ServiceID)
	s.flowPath = utils.ResolveUnixPaths(root, id)

	s.consulClient, err = consul.NewConsulAPIFromConfig(conf)
	if err != nil {
		return err
	}
	flowTk := time.NewTicker(conf.GetDuration(ConfSchedulerFlowIntervalKey))

	// 等待缓存同步完成
	storage.WaitCache()

	logging.Infof("scheduler %s is running", s.Name)

	consul.SchedulerHelper.SyncConf()
	evCh := s.watcher(s.ctx)
loop:
	for {
		select {
		case <-s.ctx.Done():
			break loop
		case ev, ok := <-evCh:
			if !ok {
				logging.Warnf("watch channel is closed")
				break loop
			}
			s.pendingPipeline(fmt.Sprintf("handle event[%v] %v", ev.Type, ev.ID), func() {
				s.handleWatchEvent(ev)
			})
		case <-ticker.C:
			s.handleKillChannels()

		case <-flowTk.C:
			flows := make(map[int]int) // map[dataid]flow
			_ = s.PipelineManager.EachAliveItem(func(i int, item *PipelineItem) error {
				if consul.SchedulerHelper.GetConf().RecorderEnabled {
					flows[item.Config.DataID] = item.Pipeline.Flow()
				}
				return nil
			})

			for dataID, f := range flows {
				if err := s.recordFlow(dataID, f); err != nil {
					logging.Errorf("failed to record flow, err:%v", err)
				}
			}
		}
	}

	ticker.Stop()
	flowTk.Stop()
	return nil
}

// Stop :
func (s *Scheduler) Stop() error {
	err := s.PipelineManager.EachAliveItem(func(dataID int, item *PipelineItem) error {
		s.waitGroup.Add(1)
		go func() {
			defer s.waitGroup.Done()
			err := item.Terminate(s.cleanUpTimeout)
			if err != nil {
				logging.Warnf("terminate pipeline %v error %v", item.Pipeline, err)
			}
		}()
		return nil
	})
	if err != nil {
		return err
	}

	s.waitGroup.Add(1)
	go func() {
		duration := s.cleanUpTimeout / 2
		logging.Warnf("cancel context in %v to kill all tasks", duration)
		time.Sleep(duration)
		s.cancelFn()
		s.waitGroup.Done()
	}()

	consul.SchedulerHelper.Close()
	return s.TaskManager.Stop()
}

// Wait :
func (s *Scheduler) Wait() (err error) {
	defer func() {
		logging.WarnIf("close store error", s.store.Close())
	}()
	err = utils.WaitOrTimeOut(s.cleanUpTimeout, func() {
		err = s.TaskManager.Wait()
	})
	if err != nil {
		return err
	}

	err = utils.WaitOrTimeOut(s.cleanUpTimeout, s.waitGroup.Wait)

	return err
}

// NewSchedulerWithTaskManager
func NewSchedulerWithTaskManager(ctx context.Context, name string, watcher Watcher, manager *define.TaskManager, services ...define.Service) (*Scheduler, error) {
	conf := config.FromContext(ctx)
	if conf == nil {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
	}
	cleanUpTimeout := conf.GetDuration(ConfSchedulerCleanUpDurationKey)
	sch := &Scheduler{
		Name:            name,
		cleanUpTimeout:  cleanUpTimeout,
		PipelineManager: NewPipelineManager(cleanUpTimeout),
		watcher:         watcher,
		TaskManager:     manager,
	}
	if len(services) > 0 {
		sch.service = services[0]
	}
	err := sch.build(ctx)
	if err != nil {
		return nil, err
	}
	return sch, nil
}

// NewScheduler
func NewScheduler(ctx context.Context, name string, watcher Watcher) (*Scheduler, error) {
	return NewSchedulerWithTaskManager(ctx, name, watcher, define.NewTaskManager())
}
