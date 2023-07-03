// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/module"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var CounterProcess = uint64(0) // 计数器，读的行数

type IProcessor interface {
	// first return value is nil: drop line
	Handle(event *module.LogEvent) (interface{}, error)
	Send(event interface{}, outputs []chan<- interface{})
}

type Processor struct {
	cfg     keyword.ProcessConfig
	ctx     context.Context
	process IProcessor

	outputs []chan<- interface{}
	input   <-chan interface{} // TODO 对接多个input?
	wg      sync.WaitGroup
}

func (client *Processor) Start() error {
	logger.Infof("Starting processor, %s", client.ID())
	go client.run()
	return nil
}

// Stop stops the input and with it all harvesters
func (client *Processor) Stop() {
}

// Wait
func (client *Processor) Wait() {
	client.wg.Wait()
}

// Reload
func (client *Processor) Reload(cfg interface{}) {
}

func (client *Processor) ID() string {
	return fmt.Sprintf("process-%d-%s", client.cfg.DataID, client.ctx.Value("taskID").(string))
}

// AddOutput add one output
func (client *Processor) AddOutput(output chan<- interface{}) {
	if output == nil {
		logger.Error("should not add nil output!")
		return
	}
	client.outputs = append(client.outputs, output)
}

// AddInput. implement module interface
func (client *Processor) AddInput(input <-chan interface{}) {
	if input == nil {
		logger.Error("should not add nil input!")
		return
	}
	client.input = input
}

func (client *Processor) run() {
	client.wg.Add(1)
	defer client.wg.Done()

	for {
		select {
		case <-client.ctx.Done():
			logger.Infof("processor quit, id: %s", client.ID())
			return
		case event := <-client.input:
			event, err := client.handle(event)
			if err != nil {
				logger.Errorf("handle event error, %v", err)
				continue
			}

			if event == nil {
				// drop data or not complete
				continue
			}

			client.send(event)
		}
	}
}

func (client *Processor) handle(event interface{}) (interface{}, error) {
	// clone the event at first, before starting filtering
	res, err := client.process.Handle(event.(*module.LogEvent))
	if err != nil {
		logger.Errorf("handle error, %v", err)
		return nil, err
	}

	// drop line
	if res == nil {
		return nil, nil
	}

	atomic.AddUint64(&CounterProcess, 1)

	return res, nil
}

func (client *Processor) send(event interface{}) {
	// transfer newmsg to next nodes
	client.process.Send(event, client.outputs)

}

func New(ctx context.Context, cfg keyword.ProcessConfig, taskType string) (module.Module, error) {
	logger.Debugf("processor cfg:[%+v]", cfg)
	var (
		p   IProcessor
		err error
	)
	if taskType == configs.TaskTypeKeyword {
		p, err = NewEventProcessor(cfg)
	} else {
		return nil, errors.Errorf("Unknown task type(%s)", taskType)
	}

	if err != nil {
		return nil, err
	}

	processor := Processor{
		cfg:     cfg,
		ctx:     ctx,
		process: p,
	}

	return &processor, nil
}
