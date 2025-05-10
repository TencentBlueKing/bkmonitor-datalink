// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb

import (
	"context"
	"sync"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
)

type Task struct {
	BkTenantID string                            // 租户id
	BizID      int                               // 业务id
	Topo       []CCSearchBizInstTopoResponseInfo //

	Start int // 当次需要开始查询的内容
	Limit int
}

// CallBackFuncType:
type CallBackFuncType func(task Task)

type TaskManager struct {
	// 待完成的任务队列, 外部可以不断往里推送任务
	JobQueue chan Task

	// worker并发任务的令牌桶
	tokenBucket chan interface{}
	maxWorker   int
	// 任务结束标志位
	ctx        context.Context
	cancelFunc context.CancelFunc
	// 回调函数的方法
	callBackFunc CallBackFuncType
	// 等待所有任务的结束的waitGroup
	wg sync.WaitGroup
	// 任务是否正在进行的标记位
	isRunning bool
	// 所有任务列表
	taskList []Task
	jobWg    sync.WaitGroup
}

func NewTaskManage(ctx context.Context, maxWorker int, callBackFunc CallBackFuncType, task []Task) (*TaskManager, error) {
	var (
		newCtx     context.Context
		cancelFunc context.CancelFunc
		m          *TaskManager
	)

	// 最大worker有效性的
	if maxWorker > MaxWorkerConfig || maxWorker <= 0 {
		logging.Infof("maxWorker->[%d] is less than 0 or more than max, max->[%d] will set.", maxWorker, MaxWorkerConfig)
		maxWorker = MaxWorkerConfig
	}

	// 注册一个context
	newCtx, cancelFunc = context.WithCancel(ctx)

	// 注意，此处没有初始化对应的队列，需要在start的时候注册
	m = &TaskManager{
		ctx:          newCtx,
		cancelFunc:   cancelFunc,
		callBackFunc: callBackFunc,
		maxWorker:    maxWorker,
		taskList:     task,
	}

	return m, nil
}

// Start: 实际的任务执行，每次新的任务执行前，都需要从令牌桶获得新的任务，确保并发的任务数量得到控制
func (m *TaskManager) Start() error {
	if m.isRunning {
		logging.Errorf("manager is already running, nothing will do.")
		return errors.Wrapf(define.ErrOperationForbidden, "manager is already running")
	}

	// 变更状态
	m.init()
	logging.Debugf("manager init done.")
	m.isRunning = true
	logging.Debugf("manager is_running now is set to true")

	// 任务可以在后台进行
	go func() {
		defer func() {
			m.isRunning = false
			logging.Debugf("manager is_running is set to false now.")
		}()
		for {
			select {
			case task := <-m.JobQueue:

				// 需要先获得一个token，然后再启动goroutines执行任务
				logging.Debugf("job got, task manager ready to wait for token to start a job")
				<-m.tokenBucket
				logging.Debugf("token got, ready to add waitGroup")

				m.wg.Add(1)
				logging.Debugf("waitGroup add done, job will start now")

				go func(taskInfo Task) {
					// goroutines完成，需要交还三个东西，一个是wg, 一个是token, 一个是任务的计数
					defer func() {
						m.wg.Done()
						logging.Debugf("manager wg done.")
						m.tokenBucket <- struct{}{}
						logging.Debugf("manager token return success.")
						m.jobWg.Done()
						logging.Debugf("manager job wg done.")
					}()

					// 实际执行任务
					logging.Debugf("ready to execute the real job.")
					m.callBackFunc(taskInfo)
					logging.Debugf("job done.")
				}(task)

			case <-m.ctx.Done():
				logging.Infof("context done, all task will stop.")
				return
			}
		}
	}()

	go func() {
		// 当任务发送完成时，可以解除这个任务
		defer func() {
			m.jobWg.Done() //
			logging.Infof("manager had sent all job, will exit now")
		}()
		// 在任务完成前，会不断的发送任务
		for i := 0; i < len(m.taskList); {
			select {
			case m.JobQueue <- m.taskList[i]:
				m.jobWg.Add(1)
				i++
				logging.Debugf("job send, job wg added")
			// 如果manager停止，需要停止发送任务
			case <-m.ctx.Done():
				logging.Infof("manager is stop, no more job will send.")
			}
		}
	}()
	// 开始时，增加一个初始化任务记录，可以理解为，发送任务也是需要等待的
	m.jobWg.Add(1)

	return nil
}

// Wait: 任务结束后，需要等待所有的任务结束完成
func (m *TaskManager) Wait() error {
	defer func() { logging.Infof("manager wait done.") }()

	logging.Debugf("start to wait task manager all task done.")
	m.wg.Wait()
	return nil
}

// WaitJob: 等待所有的任务完成
func (m *TaskManager) WaitJob() error {
	defer func() { logging.Infof("manager wait for job done.") }()

	logging.Debugf("start to wait job manager all task done.")
	m.jobWg.Wait()
	return nil
}

// Stop: 停止当前的任务
func (m *TaskManager) Stop() error {
	logging.Debugf("manager stop now")
	m.cancelFunc()
	logging.Infof("manager is stop now.")
	return nil
}

func (m *TaskManager) init() {
	if m.isRunning {
		logging.Errorf("manager is running, nothing will init.")
		return
	}

	// 重新置换新的channel，之前的任务全部丢弃
	logging.Debugf("going to init tokenBucket and job Queue")
	m.tokenBucket = make(chan interface{}, m.maxWorker)
	m.JobQueue = make(chan Task, m.maxWorker)
	logging.Infof("jobQueue and tokenBucket is init with length->[%d]", m.maxWorker)

	// 填充初始化的令牌桶
	for i := 0; i < m.maxWorker; i++ {
		m.tokenBucket <- struct{}{}
	}
	logging.Debugf("token bucket init done.")

	// 任务的wg需要重置
	m.jobWg = sync.WaitGroup{}
	logging.Debugf("job wg is replace done.")

	logging.Infof("manager init done.")
}

// GetAllTaskInfo 传入所有业务的配置信息，然后判断有哪些业务是需要继续重复获取的，而且将重复获取的任务都提前规划好
func GetAllTaskInfo(c APIClient, limit int, ccInfo models.CCInfo, fn func(monitor CCSearchHostResponseDataV3Monitor, ccInfo models.CCInfo) error) ([]Task, error) {
	var (
		// 待处理任务队列
		taskList = make([]Task, 0)
		// 当前开始计算的请求主机开始计数
		err    error
		wg     sync.WaitGroup
		taskWg sync.WaitGroup
		// 最大goroutine 数量
		maxWorker = make(chan struct{}, MaxWorkerConfig)
		// 待处理任务chan
		taskCh     = make(chan Task)
		subTaskErr error
	)
	defer close(maxWorker)
	taskWg.Add(1)
	go func() {
		defer taskWg.Done()
		for value := range taskCh {
			taskList = append(taskList, value)
		}
	}()
	if limit <= 0 {
		logging.Errorf("GetAllTaskInfo got limit->[%d] which should larger than zero", limit)
	}
	allBiz, err := c.GetSearchBusiness()
	if err != nil {
		logging.Errorf("unable to get all biz information by %v", err)
		return nil, err
	}
	wg.Add(len(allBiz))
	// 遍历获取所有的业务，计算拆分每个业务需要获取的任务
	for _, bizInfo := range allBiz {
		go func(bkTenantID string, bizID int, taskCh chan Task) {
			defer wg.Done()
			// 限制最大goroutine数量
			defer func() {
				<-maxWorker
			}()

			maxWorker <- struct{}{}
			var (
				todoTaskCount int
				bizTotalCount int
				currentStart  int
				ccHostMonitor *CCSearchHostResponseDataV3Monitor
			)
			switch ccInfo.(type) {
			case *models.CCHostInfo:

				hostRes, err := c.GetHostsByRange(bkTenantID, bizID, limit, currentStart)
				if err != nil {
					subTaskErr = err
					logging.Warnf("cc search host err by %v", err)
					return
				}
				ccHostMonitor, _ = OpenHostResInMonitorAdapter(hostRes, bizID)
			case *models.CCInstanceInfo:
				instanceRes, err := c.GetServiceInstance(bkTenantID, bizID, limit, currentStart, nil)
				if err != nil {
					subTaskErr = err
					logging.Warnf("cc search instance err by %v", err)
					return
				}
				ccHostMonitor, _ = OpenInstanceResInMonitorAdapter(instanceRes, bizID)

			}
			// 这里会分两次判断，按照实例下发和按照主机下发
			if ccHostMonitor == nil || len(ccHostMonitor.Info) == 0 {
				logging.Debugf("no host info found in biz: %v", bizID)
				return
			}

			ccTopo, _ := c.GetSearchBizInstTopo(bkTenantID, 0, bizID, 0, -1)
			// 每个业务开始的时候，当前的启动需要重置为limit的值
			currentStart = limit
			bizTotalCount = ccHostMonitor.Count

			// 如果发现整理的机器数量小于当次查询量
			if bizTotalCount <= limit {
				logging.Infof("limit->[%d] is more than biz host count->[%d], not more job will do.", limit, bizTotalCount)
			}

			// 遍历获取该业务需要多少个job
			// 如果发现当前的请求量已经大于了整个业务的整体量，那么可以退出
			for bizTotalCount > currentStart {
				todoTaskCount++
				logging.Debugf("currentStart less than total->[%d] will add Task start->[%d] limit->[%d]", bizTotalCount, currentStart, limit)
				// 增加一个新的任务内容，在当前的请求量增加limit值
				taskCh <- Task{
					BkTenantID: bkTenantID,
					BizID:      bizID,
					Start:      currentStart,
					Limit:      limit,
					Topo:       ccTopo,
				}

				// 转移到下一个阶段
				currentStart += limit
				logging.Debugf("new round currentStart->[%d] limit->[%d]", currentStart, limit)
			}
			for _, topoInfo := range ccTopo {
				MergeTopoHost(ccHostMonitor, TopoDataToCmdbLevelV3(&topoInfo))
			}
			err = fn(*ccHostMonitor, ccInfo)
			if err != nil {
				subTaskErr = err
				logging.Errorf("unable to load %v to store", ccHostMonitor)
				return

			}
			// 该业务为null
			if len(ccHostMonitor.Info) == 0 {
				logging.Debugf("no host info found in %v", bizID)
				return
			}
			logging.Infof("biz->[%d] is process done with task->[%d] now.", bizID, todoTaskCount*limit)
		}(bizInfo.BkTenantID, bizInfo.BKBizID, taskCh)
	}
	wg.Wait()
	close(taskCh)
	taskWg.Wait()
	return taskList, subTaskErr
}
