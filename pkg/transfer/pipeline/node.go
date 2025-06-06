// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// DefaultChannelBufferSize
var DefaultChannelBufferSize = 0

// BaseNode :
type BaseNode struct {
	name      string
	ctx       context.Context
	cancelFn  context.CancelFunc
	waitGroup sync.WaitGroup
	killCh    chan<- error
	noCopy    bool
}

func (n *BaseNode) SetNoCopy(noCopy bool) {
	n.noCopy = noCopy
}

// NoCopy 当该节点有多个目标传输节点时，是否要复制数据
func (n *BaseNode) NoCopy() bool {
	return n.noCopy
}

// String :
func (n *BaseNode) String() string {
	return n.name
}

// GetOutputChan :
func (n *BaseNode) GetOutputChan() <-chan define.Payload {
	return nil
}

// ConnectFrom :
func (n *BaseNode) ConnectFrom(<-chan define.Payload) error {
	return define.ErrNotImplemented
}

// ConnectTo :
func (n *BaseNode) ConnectTo(Node) error {
	return define.ErrNotImplemented
}

// Kill :
func (n *BaseNode) Kill(err error) {
	n.waitGroup.Add(1)
	go func() {
		select {
		case <-n.ctx.Done():
			logging.Warnf("%v abort kill error %v because of context done", n, err)
		case n.killCh <- err:
			logging.Warnf("%v sent kill err: %v", n, err)
		}
		n.waitGroup.Done()
	}()
}

// Start :
func (n *BaseNode) Start(killChan chan<- error) {
	n.waitGroup.Add(1)
	n.killCh = killChan
}

// Stop :
func (n *BaseNode) Stop() error {
	n.cancelFn()
	n.waitGroup.Done()

	logging.Infof("node %v stopped", n)
	return nil
}

// Wait :
func (n *BaseNode) Wait() error {
	n.waitGroup.Wait()
	logging.Infof("node %v done", n)
	return nil
}

// NewBaseNode :
func NewBaseNode(ctx context.Context, cancelFn context.CancelFunc, name string) *BaseNode {
	return &BaseNode{
		name:     name,
		ctx:      ctx,
		cancelFn: cancelFn,
	}
}

// SimpleNode :
type SimpleNode struct {
	*BaseNode
	inputCh  <-chan define.Payload
	outputCh chan define.Payload
}

// GetOutputChan :
func (n *SimpleNode) GetOutputChan() <-chan define.Payload {
	return n.outputCh
}

// ConnectFrom :
func (n *SimpleNode) ConnectFrom(ch <-chan define.Payload) error {
	if n.inputCh != nil {
		return errors.Wrapf(define.ErrOperationForbidden, "input channel has been set")
	}
	n.inputCh = ch
	return nil
}

// ConnectTo :
func (n *SimpleNode) ConnectTo(node Node) error {
	return node.ConnectFrom(n.outputCh)
}

// Wait :
func (n *SimpleNode) Wait() error {
	logging.Infof("node %v is waiting", n)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if n.outputCh != nil {
			for d := range n.outputCh {
				logging.Warnf("%v drop payload %#v", n, d)
			}
		}
		wg.Done()
	}()
	err := n.BaseNode.Wait()

	if n.outputCh != nil {
		close(n.outputCh)
	}
	wg.Wait()

	return err
}

// NewSimpleNode :
func NewSimpleNode(ctx context.Context, cancelFn context.CancelFunc, name string) *SimpleNode {
	return &SimpleNode{
		BaseNode: NewBaseNode(ctx, cancelFn, name),
	}
}

// FrontendNode :
type FrontendNode struct {
	*SimpleNode
	frontend  define.Frontend
	waitDelay time.Duration
}

// Start :
func (n *FrontendNode) Start(killChan chan<- error) {
	logging.Infof("frontend %v is starting", n.frontend)
	defer logging.Infof("frontend %v started", n.frontend)

	n.SimpleNode.Start(killChan)

	n.waitGroup.Add(1)
	go func() {
		defer n.waitGroup.Done()
		defer utils.RecoverError(func(e error) {
			logging.Errorf("killing frontend %v because of panic %+v", n, e)
			n.Kill(e)
		})
		logging.Infof("frontend %v is running", n.frontend)
		n.frontend.Pull(n.outputCh, killChan)
		err := n.frontend.Close()
		if err != nil {
			logging.Infof("frontend %v close error: %v", n.frontend, err)
		} else {
			logging.Infof("frontend %v finished", n.frontend)
		}
		_, done := utils.TimeoutOrContextDone(n.ctx, time.After(n.waitDelay))
		if !done {
			sendKillChan(n.ctx, killChan, errors.Wrapf(define.ErrTimeout, "frontend %v finished", n))
		}
	}()
}

// NewFrontendNode :
func NewFrontendNode(ctx context.Context, cancelFn context.CancelFunc, frontend define.Frontend, waitDelay time.Duration) *FrontendNode {
	node := &FrontendNode{
		SimpleNode: NewSimpleNode(ctx, cancelFn, fmt.Sprintf("+:%v", frontend)),
		frontend:   frontend,
		waitDelay:  waitDelay,
	}
	node.outputCh = make(chan define.Payload, DefaultChannelBufferSize)
	return node
}

// BackendNode :
type BackendNode struct {
	*SimpleNode
	backend  define.Backend
	multiNum int
}

// ConnectTo :
func (n *BackendNode) ConnectTo(node Node) error {
	if n.outputCh == nil {
		n.outputCh = make(chan define.Payload, DefaultChannelBufferSize)
	}
	return n.SimpleNode.ConnectTo(node)
}

func (n *BackendNode) send(payload define.Payload) error {
	select {
	case n.outputCh <- payload:
		logging.Debugf("backend %s sending payload: %v", n.backend, payload)
	case <-n.ctx.Done():
		logging.Infof("backend %v context done", n.backend)
	}
	return nil
}

// Start :
func (n *BackendNode) Start(killChan chan<- error) {
	logging.Infof("backend %v is starting", n.backend)
	defer logging.Infof("backend %v started", n.backend)

	n.SimpleNode.Start(killChan)
	innerWg := new(sync.WaitGroup)
	for index := 0; index < n.multiNum; index++ {
		innerWg.Add(1)
		n.waitGroup.Add(1)
		go func(loopIndex int) {
			defer n.waitGroup.Done()
			defer innerWg.Done()
			defer utils.RecoverError(func(e error) {
				logging.Errorf("killing backend %v:%d because of panic %+v", n, loopIndex, e)
				n.Kill(e)
			})
			logging.Infof("backend %v:%d is running", n.backend, loopIndex)
		loop:
			for {
				select {
				case payload, ok := <-n.inputCh:
					if !ok {
						logging.Infof("backend %v:%d input channel closed", n.backend, loopIndex)
						break loop
					}
					logging.Debugf("backend %v:%d received data: %v", n.backend, loopIndex, payload)
					n.backend.Push(payload, killChan)
					logging.Debugf("backend %v:%d pushed: %#v", n.backend, loopIndex, payload)
					if n.outputCh != nil {
						sendKillChan(n.ctx, n.killCh, n.send(payload))
					}

				case <-n.ctx.Done():
					logging.Infof("backend %v:%d context done", n.backend, loopIndex)
					break loop
				}
			}
		}(index)
	}
	// 所有输入关闭后再调用backend的close
	n.waitGroup.Add(1)
	go func() {
		defer n.waitGroup.Done()
		innerWg.Wait()
		err := n.backend.Close()
		if err != nil {
			logging.Infof("backend %v close error: %v", n.backend, err)
		} else {
			logging.Infof("backend %v finished", n.backend)
		}
	}()
}

// NewBackendNode :
func NewBackendNode(ctx context.Context, cancelFn context.CancelFunc, backend define.Backend) *BackendNode {
	rtConfig := config.ResultTableConfigFromContext(ctx)
	multiNum := 1
	if rtConfig != nil {
		multiNum = rtConfig.MultiNum
	}
	node := &BackendNode{
		SimpleNode: NewSimpleNode(ctx, cancelFn, fmt.Sprintf("$:%v", backend)),
		backend:    backend,
		multiNum:   multiNum,
	}
	return node
}

// ProcessNode :
type ProcessNode struct {
	*SimpleNode
	handleTimeObserver *monitor.TimeObserver
	processor          define.DataProcessor
}

// String :
func (n *ProcessNode) String() string {
	return n.processor.String()
}

// Start :
func (n *ProcessNode) Start(killChan chan<- error) {
	logging.Infof("processor %v is starting", n.processor)
	defer logging.Infof("processor %v started", n.processor)

	n.SimpleNode.Start(killChan)
	// 调试技巧 如何调试goroutine
	n.waitGroup.Add(1)
	go func() {
		defer n.waitGroup.Done()
		defer utils.RecoverError(func(e error) {
			logging.Errorf("killing process %v because of panic %+v", n, e)
			n.Kill(e)
		})

		logging.Infof("processor %v is running", n.processor)
	loop:
		for {
			select {
			case payload, ok := <-n.inputCh:
				if !ok {
					logging.Infof("processor %v input channel closed", n.processor)
					break loop
				}

				logging.Debugf("processor %v received data: %v", n.processor, payload)
				ObserverRecord := n.handleTimeObserver.Start()
				n.processor.Process(payload, n.outputCh, killChan)
				ObserverRecord.Finish()
				logging.Debugf("processor %v processed: %#v", n.processor, payload)
			case <-n.ctx.Done():
				logging.Infof("processor %v context done", n.processor)
				break loop
			}
		}
		n.processor.Finish(n.outputCh, killChan)
		logging.Infof("processor %v finished", n.processor)
	}()

	if n.processor.Poll() > 0 {
		go func() {
			ticker := time.NewTicker(n.processor.Poll())
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					n.processor.Process(nil, n.outputCh, killChan)

				case <-n.ctx.Done():
					n.processor.Process(nil, n.outputCh, killChan) // 结束前清空
					return
				}
			}
		}()
	}
}

// NewProcessNode :
func NewProcessNode(ctx context.Context, cancelFn context.CancelFunc, processor define.DataProcessor) *ProcessNode {
	pipelineConfig := config.PipelineConfigFromContext(ctx)
	runtimeConfig := config.RuntimeConfigFromContext(ctx)
	name := processor.String()
	if runtimeConfig != nil {
		name = fmt.Sprintf("%d:%s", runtimeConfig.PipelineCount, name)
		processor.SetIndex(runtimeConfig.PipelineCount)
	}

	node := &ProcessNode{
		SimpleNode: NewSimpleNode(ctx, cancelFn, name),
		processor:  processor,
		handleTimeObserver: monitor.NewTimeObserver(define.MonitorProcessorHandleDuration.With(prometheus.Labels{
			"id":       strconv.Itoa(pipelineConfig.DataID),
			"pipeline": name,
		})),
	}
	node.outputCh = make(chan define.Payload, DefaultChannelBufferSize)
	return node
}
