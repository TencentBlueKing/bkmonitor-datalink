// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trap

import (
	"context"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Sender struct {
	tasks.CmdbEventSender
	counterMap  map[string]*EventCounter
	mux         *sync.RWMutex
	Period      time.Duration
	IsAggregate bool
	output      chan<- define.Event
	input       chan *Event
	ctx         context.Context
	closeChan   chan struct{}
	Label       []configs.Label // 配置的labels
}

type EventCounter struct {
	event   *Event
	count   uint32
	hashKey string
}

type OutputData struct {
	data common.MapStr
}

// IgnoreCMDBLevel :
func (o *OutputData) IgnoreCMDBLevel() bool { return false }

func (o *OutputData) AsMapStr() common.MapStr {
	return o.data
}

func (o *OutputData) GetType() string {
	return define.ModuleTrap
}

func NewSender(period time.Duration, isAggregate bool, output chan<- define.Event, ctx context.Context) *Sender {
	logger.Debugf("new sender with period:[%s], isaggregate:[%v]", period.String(), isAggregate)
	return &Sender{
		counterMap:  make(map[string]*EventCounter, 1),
		mux:         new(sync.RWMutex),
		Period:      period,
		IsAggregate: isAggregate,
		output:      output,
		input:       nil,
		ctx:         ctx,
		closeChan:   make(chan struct{}),
	}
}

func (s *Sender) SetInput(input chan *Event) {
	s.input = input
}

func (s *Sender) SetOutput(output chan define.Event) {
	// s.output = output
}

func (s *Sender) sendTrap(event *Event) {
	s.mux.Lock()
	defer s.mux.Unlock()
	logger.Debugf("receive trap %+v", event)
	// 运算得hash，去除运行时间
	hashKey := utils.GeneratorHashKey([]string{event.hashContent})
	eventCounter, ok := s.counterMap[hashKey]
	if !ok {
		s.counterMap[hashKey] = &EventCounter{
			event:   event,
			count:   event.metrics["count"],
			hashKey: hashKey,
		}
		return
	}
	eventCounter.count++
}

func (s *Sender) cleanCache() {
	s.mux.Lock()
	defer s.mux.Unlock()
	logger.Debug("clean trap cache")
	s.counterMap = make(map[string]*EventCounter, 1)
}

func (s *Sender) send() {
	s.mux.Lock()
	defer s.mux.Unlock()
	for _, ec := range s.counterMap {
		ec.event.metrics["count"] = ec.count
		// 填充cmdb层级信息
		s.Label = ec.event.labels
		data := s.DuplicateRecordByCMDBLevel(ec.event.toMapStr(), s.Label)
		logger.Debugf("send output data %v", data)
		s.output <- &OutputData{data: common.MapStr{
			"dataid": ec.event.dataid,
			"data":   data,
		}}
	}
}

func (s *Sender) Run() {
	logger.Info("trap sender run")
	ticker := time.NewTicker(s.Period)
	defer func() {
		// 退出之前上报缓存区内容
		logger.Info("flush trap cache before stop")
		s.send()
		s.cleanCache()
		logger.Info("flush trap cache over")
	}()
loop:
	for {
		select {
		case <-ticker.C:
			if !s.IsAggregate {
				continue
			}
			logger.Debugf("send traps in period:%s", s.Period.String())
			s.send()
			s.cleanCache()
		case event := <-s.input:
			s.sendTrap(event)
			if s.IsAggregate {
				continue
			}
			s.send()
			s.cleanCache()
		case <-s.closeChan:
			logger.Info("trap sender closed")
			break loop
		case <-s.ctx.Done():
			logger.Info("trap sender done")
			break loop
		}
	}
}

func (s *Sender) Stop() {
	close(s.closeChan)
}
