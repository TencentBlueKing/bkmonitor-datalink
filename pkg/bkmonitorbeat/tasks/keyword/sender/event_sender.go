// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sender

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type EventCounter struct {
	EventName  string                 // 日志采集事件名
	Count      int                    // 事件产生计数器
	LastLog    string                 // 最后日志
	Dimensions map[string]interface{} // 事件相关维度
}

// toMapStr: 将记录直接转换为对应的记录
func (e *EventCounter) toMapStr() common.MapStr {
	return map[string]interface{}{
		EventEventNameKey: e.EventName,
		EventEventKey: map[string]interface{}{
			"count":   e.Count,
			"content": e.LastLog,
		},
		EventDimensionKey: e.Dimensions,
	}
}

// reset: 重置记录，将count和log归零
func (e *EventCounter) reset() {
	e.Count = 0
	e.LastLog = ""
}

// addCount: 增加一条新的日志记录，并将旧的日志信息替换
func (e *EventCounter) addCount(log string) {
	e.Count++
	e.LastLog = log
}

// 时间单位转换，默认转为了毫秒级别
func transferUnitToInt(u string) int64 {
	switch u {
	case "s":
		return 1
	case "ms":
		return 1e3
	case "us":
		return 1e6
	case "ns":
		return 1e9
	default:
		return 1e3
	}
}

type appendFunc func(common.MapStr)

// EventSender: 日志关键字的事件发送
type EventSender struct {
	tasks.CmdbEventSender
	cfg       keyword.SendConfig       // 发送任务配置
	eventChan chan<- define.Event      // 发送client，屏蔽gse发送逻辑
	input     <-chan interface{}       // 数据接收channel，用于接收上一层processor的结果
	wg        sync.WaitGroup           // 等待信号，用于退出时可以等待清理
	cache     map[string]*EventCounter // 缓存计数器
	ticker    *time.Ticker             // 计时器，用于计时周期发送汇聚结果

	lock         sync.Mutex // cache锁，用于供汇聚发送时和数据写入时的协调，由于发送时需要清理cache，存在写行为；接受数据时，需要修改cache，存在写行为；所以此处只有一个写锁
	isRunning    bool       // 任务是否已经启动，防止任务重入导致有多次发送
	timeUnitBase int64      // 时间单位调整基数

	ctx context.Context
}

func NewEventSender(ctx context.Context, config keyword.SendConfig, eChan chan<- define.Event) *EventSender {
	return &EventSender{
		cfg:       config,
		eventChan: eChan,
		cache:     make(map[string]*EventCounter),

		ctx: ctx,
	}
}

func (s *EventSender) ID() string {
	return fmt.Sprintf("sender-%d", s.cfg.DataID)
}

func (s *EventSender) AddInput(input <-chan interface{}) {
	s.input = input
}

// AddOutput  由于Sender是最后一个环节了，所以不需要将output保留
func (s *EventSender) AddOutput(output chan<- interface{}) {}

func (s *EventSender) Start() error {
	// 1. 判断任务是否已经启动
	if s.isRunning {
		return errors.Wrapf(errors.New("task already running"), "%s", s.ID())
	}

	// 2. 启动周期任务，周期调度发送cache中的内容
	s.timeUnitBase = transferUnitToInt(s.cfg.TimeUnit)
	logger.Debugf("task->[%s] unit->[%s] and base->[%d]", s.ID(), s.cfg.TimeUnit, s.timeUnitBase)
	go s.backGroupTask()
	logger.Infof("task->[%s] backGroup task is running now.", s.ID())

	return nil
}

func (s *EventSender) Stop() {
	logger.Warnf("%s now is stop.", s.ID())
}

func (s *EventSender) Wait() {
	logger.Debugf("%s now is going to waiting for tasks.", s.ID())
	s.wg.Wait()
	logger.Infof("%s wait done.", s.ID())
}

func (s *EventSender) Reload(interface{}) {}

func (s *EventSender) backGroupTask() {
	var data interface{}

	// 进入时，需要先增加任务的计数
	s.wg.Add(1)
	defer func() {
		// 退出前，需要做最后一次的数据上报清理
		s.flushCache()
		logger.Infof("task->[%s] flush data before exit success.", s.ID())
		s.isRunning = false
		logger.Debugf("task->[%s] is_running set to false now.", s.ID())
		s.wg.Done()
		logger.Infof("task->[%s] backGroupTask is exit now.", s.ID())
	}()

	s.ticker = time.NewTicker(s.cfg.ReportPeriod)
	defer s.ticker.Stop()
	logger.Debugf("task->[%s] ticker is set to period->[%d]", s.ID(), s.cfg.ReportPeriod)

loop:
	for {
		select {
		case data = <-s.input:
			// 接收到新的任务，需要处理
			logger.Debugf("task->[%s] got new data.", s.ID())
			s.cacheResult(data)
		case <-s.ticker.C:
			// 定期发送事件
			logger.Debugf("task->[%s] bell ringing, will flush cache", s.ID())
			s.flushCache()
		case <-s.ctx.Done():
			// 发现需要退出了，中断循环
			logger.Infof("task->[%s] is close now, will clean everything.", s.ID())
			break loop
		}
	}
}

// cacheResult: 处理processor推送过来的内容，并放入到缓存中
func (s *EventSender) cacheResult(rawResult interface{}) {
	var (
		ok            bool
		err           error
		keywordResult keyword.KeywordTaskResult
		hashKey       string
		counter       *EventCounter
	)

	// 1. 尝试转换类型
	if keywordResult, ok = rawResult.(keyword.KeywordTaskResult); !ok {
		logger.Warnf("task->[%s] got wrong type result which cannot convert to keywordResult, will drop it", s.ID())
		return
	}

	if hashKey, err = keywordResult.MakeKey(); err != nil {
		logger.Warnf("task->[%s] got keywordResult which cannot make key for->[%s], will drop it.", s.ID(), err)
		return
	}

	// 2. 要开始接触数据了，此时需要增加锁防止重入导致map异常
	logger.Debugf("task->[%s] hashKey->[%s] start to wait for lock", s.ID(), hashKey)
	s.lock.Lock()
	logger.Debugf("task->[%s] hashKey->[%s] got lock now.", s.ID(), hashKey)
	defer func() {
		s.lock.Unlock()
		logger.Debugf("task->[%s] hashKey->[%s] release lock succuess.", s.ID(), hashKey)
	}()

	// 3. 更新数据写入到cache中
	if counter, ok = s.cache[hashKey]; ok {
		// 3.1 判断是否已经存在，如果存在则修改count和log即可
		counter.addCount(keywordResult.Log)
		logger.Debugf("task->[%s] hashKey->[%s] update log info success.", s.ID(), hashKey)
		return
	}

	// 3.2 判断如果不存在，则需要创建一个新的counter
	// 需要将string先转换为interface，方便后续使用
	tempDimension := make(map[string]interface{})
	for k, v := range keywordResult.Dimensions {
		tempDimension[k] = v
	}

	// 固定维度，目前只有file_path(文件路径)
	tempDimension["file_path"] = keywordResult.FilePath

	counter = &EventCounter{
		EventName:  keywordResult.RuleName,
		Count:      1,
		LastLog:    keywordResult.Log,
		Dimensions: tempDimension,
	}

	s.cache[hashKey] = counter
	logger.Infof("task->[%s] hashKey->[%s] get new counter", s.ID(), hashKey)
}

// flushCache: 清理发送缓存内容
func (s *EventSender) flushCache() {
	var (
		eventList = make([]common.MapStr, 0)
		eventMap  common.MapStr
		records   []common.MapStr
	)

	// 获取锁
	logger.Debugf("task %s is going to acquired lock to flush cache.", s.ID())
	s.lock.Lock()
	defer func() {
		s.lock.Unlock()
		logger.Debugf("task %s lock is released.", s.ID())
	}()
	logger.Infof("task: %s acquired lock, flush data now", s.ID())

	// 遍历所有的cache内容，打包构造成为事件发送
	for hashKey, counter := range s.cache {
		logger.Debugf("task->[%s] got hashKey->[%s] going to process it.", s.ID(), hashKey)
		eventMap = counter.toMapStr()
		logger.Debugf("task->[%s] hashKey->[%s] got eventMap->[%s] going to append time and default dimensions", s.ID(), hashKey, eventMap)

		s.appendInfo(eventMap)
		logger.Debugf("task->[%s] hashKey->[%s] append info success now is->[%s]", s.ID(), hashKey, eventMap)

		// 追加CMDB的信息，并将一条信息复制成为多条消息发送
		records = s.DuplicateRecordByCMDBLevel(eventMap, s.cfg.Label)
		logger.Debugf("task->[%s] hashKey->[%s] got records->[%d] after cmdb_level process", s.ID(), hashKey, len(records))
		eventList = append(eventList, records...)
		logger.Debugf("task->[%s] hashKey->[%s] append count->[%d] to eventList", s.ID(), hashKey, len(eventList))

		// 判断是否有超过发送上限，需要先发送一波信息
		if len(eventList) >= s.cfg.PackageCount {
			logger.Debugf("task->[%s] now has event->[%d] more than count->[%d] events, will send",
				s.ID(), len(eventList), s.cfg.PackageCount)

			go func(eventList []common.MapStr) {
				s.send(eventList)
			}(eventList)

			eventList = make([]common.MapStr, 0)
			logger.Debugf("task->[%s] round task send activated, clean eventList.", s.ID())
		}
	}

	// 发送剩余的事件
	if len(eventList) != 0 {
		logger.Debugf("task->[%s] remain some log count->[%d], will send it.", s.ID(), len(eventList))

		go func(eventList []common.MapStr) {
			s.send(eventList)
		}(eventList)

		logger.Debugf("task->[%s] remain log sent activated.", s.ID())
	}

	// 此时已经发送成功了，所以再次遍历所有的cache将cache中的数据清空
	s.cache = make(map[string]*EventCounter)
	logger.Debugf("task->[%s] cache reset success.", s.ID())
}

func (s *EventSender) appendInfo(m common.MapStr) {
	// 需要追加的函数集合
	appendFuncList := []appendFunc{
		s.appendTimeStamp,
		s.appendTarget,
	}

	// 逐一的追加信息
	for _, appendF := range appendFuncList {
		appendF(m)
	}
}

// appendTimeStamp: 追加时间（毫秒单位）
func (s *EventSender) appendTimeStamp(m common.MapStr) {
	m[EventTimeStampKey] = time.Now().Unix() * s.timeUnitBase
	logger.Infof("task->[%s] append timestamp->[%d] success.", s.ID(), m[EventTimeStampKey])
}

// appendTarget: 追加监控目标配置
func (s *EventSender) appendTarget(m common.MapStr) {
	m[EventTargetKey] = s.cfg.Target
	logger.Infof("task->[%s] append target->[%s] success.", s.ID(), m[EventTargetKey])
}

// send: 调用client发送数据
func (s *EventSender) send(data []common.MapStr) {
	logger.Debugf("task->[%s] total got event->[%d] this time.", s.ID(), len(data))
	s.eventChan <- OutputData{data: common.MapStr{
		"dataid": s.cfg.DataID,
		"data":   data,
	}}
	logger.Infof("task->[%s] send event->[%d] success.", s.ID(), len(data))
}
