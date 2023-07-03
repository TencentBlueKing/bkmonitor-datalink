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
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/emirpasic/gods/lists/doublylinkedlist"
	"github.com/emirpasic/gods/sets/hashset"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// BaseConnector :
type BaseConnector struct {
	*SimpleNode
	stop uint32
}

func (c *BaseConnector) markStop() {
	atomic.StoreUint32(&c.stop, 1)
}

func (c *BaseConnector) sendTo(payload define.Payload, ch chan<- define.Payload) {
	if atomic.LoadUint32(&c.stop) > 0 {
		return
	}
	select {
	case <-c.ctx.Done():
		return
	case ch <- payload:
		return
	}
}

// NewBaseConnector :
func NewBaseConnector(ctx context.Context) *BaseConnector {
	ctx, cancel := context.WithCancel(ctx)
	return &BaseConnector{
		SimpleNode: NewSimpleNode(ctx, cancel, "&"),
	}
}

// FanOutConnector 一对多节点连接器基础类
type MultiOutputConnector struct {
	*BaseConnector
	inputNode   Node
	outputNodes *hashset.Set
	outputs     []chan define.Payload
	once        sync.Once
}

func (c *MultiOutputConnector) Close() {
	c.markStop()
	c.once.Do(func() {
		for _, ch := range c.outputs {
			close(ch)
		}
	})
}

// Nodes :
func (c *MultiOutputConnector) Nodes() []Node {
	nodes := make([]Node, c.outputNodes.Size())
	for key, value := range c.outputNodes.Values() {
		nodes[key] = value.(Node)
	}
	return nodes
}

// String :
func (c *MultiOutputConnector) String() string {
	var (
		buffer bytes.Buffer
		err    error
	)
	_, err = fmt.Fprintf(&buffer, "(%v => ", c.inputNode)
	logging.PanicIf(err)
	values := c.outputNodes.Values()
	outputSize := c.outputNodes.Size()
	for index, value := range values {
		output := value.(Node)
		if index < outputSize-1 {
			_, err = fmt.Fprintf(&buffer, "%v, ", output)
			logging.PanicIf(err)
		} else {
			_, err = fmt.Fprintf(&buffer, "%v", output)
			logging.PanicIf(err)
		}
	}
	_, err = fmt.Fprintf(&buffer, ")")
	logging.PanicIf(err)
	return buffer.String()
}

// ConnectFrom :
func (c *MultiOutputConnector) ConnectFrom(ch <-chan define.Payload) error {
	if ch != c.inputCh {
		return errors.Wrapf(define.ErrOperationForbidden, "input channel is invalid")
	}
	return nil
}

// ConnectTo :
func (c *MultiOutputConnector) ConnectTo(n Node) error {
	if c.outputNodes.Contains(n) {
		return errors.Wrapf(define.ErrItemAlreadyExists, "%v node %v already exists ", c, n)
	}

	ch := make(chan define.Payload)
	err := n.ConnectFrom(ch)
	if err != nil {
		return err
	}
	c.outputNodes.Add(n)
	c.outputs = append(c.outputs, ch)
	return nil
}

// Start :
func (c *MultiOutputConnector) Start(killChan chan<- error) {
	logging.Warn("not implemented function called")
}

// RoundRobinConnector 一对多节点连接器，将数据轮流分发给后面节点，每条数据只给到一个节点
type RoundRobinConnector struct {
	*MultiOutputConnector
}

// Start :
func (c *RoundRobinConnector) Start(killChan chan<- error) {
	c.BaseConnector.Start(killChan)
	// 提高分发速度
	for i := 0; i < define.Concurrency(); i++ {
		c.waitGroup.Add(1)
		go func() {
			defer c.waitGroup.Done()
			defer utils.RecoverError(func(e error) {
				logging.Errorf("fan out connector start panic: %+v", e)
			})
			count := 0
			length := len(c.outputs)
		loop:
			for {
				select {
				case <-c.ctx.Done():
					logging.Infof("fan out connector %v context done", c)
					break loop
				case payload, ok := <-c.inputCh:
					if !ok {
						logging.Infof("fan out connector %v input channel closed", c)
						break loop
					}
					logging.Debugf("%v fan out %v", c, payload)
					count++
					index := count % length
					if index == 0 && count/length > 500 {
						count = 0
					}
					outputCh := c.outputs[index]
					logging.IgnorePanics(func() {
						c.sendTo(payload, outputCh)
					})
				}
			}
			c.Close()
		}()
	}
}

// NewRoundRobinConnector :
func NewRoundRobinConnector(ctx context.Context, input Node) *RoundRobinConnector {
	connector := &RoundRobinConnector{
		MultiOutputConnector: &MultiOutputConnector{
			BaseConnector: NewBaseConnector(ctx),
			inputNode:     input,
			outputNodes:   hashset.New(),
		},
	}
	connector.inputCh = input.GetOutputChan()

	return connector
}

// FanOutConnector 一对多节点连接器，将每条数据都进行复制，发送给后面的所有节点
type FanOutConnector struct {
	*MultiOutputConnector
}

// Start :
func (c *FanOutConnector) Start(killChan chan<- error) {
	c.BaseConnector.Start(killChan)
	// 提高分发速度
	for i := 0; i < define.Concurrency(); i++ {
		c.waitGroup.Add(1)
		go func() {
			defer c.waitGroup.Done()
			defer utils.RecoverError(func(e error) {
				logging.Errorf("fan out connector start panic: %+v", e)
			})
		loop:
			for {
				select {
				case <-c.ctx.Done():
					logging.Infof("fan out connector %v context done", c)
					break loop
				case payload, ok := <-c.inputCh:
					if !ok {
						logging.Infof("fan out connector %v input channel closed", c)
						break loop
					}
					logging.Debugf("%v fan out %v", c, payload)
					for _, outputCh := range c.outputs {
						logging.IgnorePanics(func() {
							c.sendTo(payload, outputCh)
						})
					}
				}
			}
			c.Close()
		}()
	}
}

// NewFanOutConnector :
func NewFanOutConnector(ctx context.Context, input Node) *FanOutConnector {
	connector := &FanOutConnector{
		MultiOutputConnector: &MultiOutputConnector{
			BaseConnector: NewBaseConnector(ctx),
			inputNode:     input,
			outputNodes:   hashset.New(),
		},
	}
	connector.inputCh = input.GetOutputChan()

	return connector
}

// FanInConnector 多对一节点连接器，将前面所有节点的数据都聚合到当前节点处理
type FanInConnector struct {
	*BaseConnector
	inputs     []<-chan define.Payload
	inputNodes *hashset.Set
	outputNode Node
	output     chan define.Payload
	started    bool
}

// String :
func (c *FanInConnector) String() string {
	var (
		buffer bytes.Buffer
		err    error
	)
	_, err = fmt.Fprintf(&buffer, "(")
	logging.PanicIf(err)
	values := c.inputNodes.Values()
	inputSize := c.inputNodes.Size()
	for index, value := range values {
		input := value.(Node)
		if index < inputSize-1 {
			_, err = fmt.Fprintf(&buffer, "%v, ", input)
			logging.PanicIf(err)
		} else {
			_, err = fmt.Fprintf(&buffer, "%v", input)
			logging.PanicIf(err)
		}
	}
	_, err = fmt.Fprintf(&buffer, "=> %v)", c.outputNode)
	logging.PanicIf(err)

	return buffer.String()
}

// Nodes :
func (c *FanInConnector) Nodes() []Node {
	return []Node{c.outputNode}
}

// ConnectFrom :
func (c *FanInConnector) ConnectFrom(ch <-chan define.Payload) error {
	c.inputs = append(c.inputs, ch)
	return nil
}

// ConnectTo :
func (c *FanInConnector) ConnectTo(n Node) error {
	if c.outputNode != nil {
		return errors.Wrapf(define.ErrOperationForbidden, "connector %v output node %v has been set when connect %v", c, c.outputNode, n)
	}
	ch := make(chan define.Payload)
	err := n.ConnectFrom(ch)
	if err != nil {
		return err
	}
	c.outputNode = n
	c.output = ch
	return nil
}

func (c *FanInConnector) Start(killChan chan<- error) {
	// Fanin机制与visit有冲突，会导致Start重复调用，这里控制它只真正启动一次
	if c.started {
		// 这里调用BaseConnector.Start是因为在transfer停止时会调用相同次数的BaseConnector.Stop
		// 不进行该调用会导致计数器产生负数而panic
		c.BaseConnector.Start(killChan)
		return
	}
	c.start(killChan)
	c.started = true
}

// Start :
func (c *FanInConnector) start(killChan chan<- error) {
	var wg sync.WaitGroup
	c.BaseConnector.Start(killChan)
	// 启动里面的node,否则数据不会被处理
	c.outputNode.Start(killChan)
	for _, input := range c.inputs {
		for i := 0; i < define.Concurrency(); i++ {
			c.waitGroup.Add(1)
			wg.Add(1)
			go func(ch <-chan define.Payload) {
				defer wg.Done()
				defer c.waitGroup.Done()
				defer utils.RecoverError(func(e error) {
					logging.Errorf("fan in connector start panic: %+v", e)
				})
			loop:
				for {
					select {
					case <-c.ctx.Done():
						logging.Infof("fan in connector %v context done", c)
						break loop
					case payload, ok := <-ch:
						if !ok {
							logging.Infof("fan in connector %v input channel closed", c)
							break loop
						}
						logging.IgnorePanics(func() {
							c.sendTo(payload, c.output)
						})
					}
				}
			}(input)
		}
	}

	c.waitGroup.Add(1)
	go func() {
		defer c.waitGroup.Done()
		wg.Wait()
		c.markStop()
		close(c.output)
	}()
}

// NewFanInConnector :
func NewFanInConnector(ctx context.Context, output Node) *FanInConnector {
	connector := &FanInConnector{
		BaseConnector: NewBaseConnector(ctx),
		inputNodes:    hashset.New(),
	}
	err := connector.ConnectTo(output)
	logging.PanicIf(err)
	return connector
}

// ChainConnector :
type ChainConnector struct {
	*BaseNode
	nodes *doublylinkedlist.List
}

// String :
func (c *ChainConnector) String() string {
	var (
		buffer bytes.Buffer
		err    error
	)
	_, err = fmt.Fprintf(&buffer, "{")
	logging.PanicIf(err)
	iterator := c.nodes.Iterator()
	nodeSize := c.nodes.Size()
	for iterator.Next() {
		value := iterator.Value()
		node := value.(Node)
		index := iterator.Index()
		if index < nodeSize-1 {
			_, err = fmt.Fprintf(&buffer, "%v, ", node)
			logging.PanicIf(err)
		} else {
			_, err = fmt.Fprintf(&buffer, "%v", node)
			logging.PanicIf(err)
		}
	}
	_, err = fmt.Fprintf(&buffer, "}")
	logging.PanicIf(err)
	return buffer.String()
}

// Nodes :
func (c *ChainConnector) Nodes() []Node {
	nodes := make([]Node, c.nodes.Size())
	iterator := c.nodes.Iterator()
	for iterator.Next() {
		value := iterator.Value()
		node := value.(Node)
		nodes = append(nodes, node)
	}
	return nodes
}

// ForEach :
func (c *ChainConnector) ForEach(fn func(i int, n Node) error) error {
	it := c.nodes.Iterator()
	for it.Next() {
		value := it.Value()
		node := value.(Node)
		err := fn(it.Index(), node)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetFirst :
func (c *ChainConnector) GetFirst() Node {
	it := c.nodes.Iterator()
	if !it.First() {
		return nil
	}
	value := it.Value()
	return value.(Node)
}

// GetLast :
func (c *ChainConnector) GetLast() Node {
	it := c.nodes.Iterator()
	if !it.Last() {
		return nil
	}
	value := it.Value()
	return value.(Node)
}

// Start :
func (c *ChainConnector) Start(killChan chan<- error) {
	c.BaseNode.Start(killChan)
	utils.CheckError(c.ForEach(func(i int, n Node) error {
		n.Start(killChan)
		return nil
	}))
}

// Stop :
func (c *ChainConnector) Stop() error {
	err := c.ForEach(func(i int, n Node) error {
		return n.Stop()
	})
	if err != nil {
		return err
	}
	return c.BaseNode.Stop()
}

// Wait :
func (c *ChainConnector) Wait() error {
	err := c.ForEach(func(i int, n Node) error {
		return n.Wait()
	})
	if err != nil {
		return err
	}
	return c.BaseNode.Wait()
}

// GetOutputChan :
func (c *ChainConnector) GetOutputChan() <-chan define.Payload {
	node := c.GetLast()
	if node == nil {
		return nil
	}
	return node.GetOutputChan()
}

// ConnectFrom :
func (c *ChainConnector) ConnectFrom(ch <-chan define.Payload) error {
	node := c.GetFirst()
	if node == nil {
		return define.ErrItemNotFound
	}
	return node.ConnectFrom(ch)
}

// ConnectMany :
func (c *ChainConnector) ConnectMany(nodes []Node) error {
	for _, node := range nodes {
		err := c.Connect(node)
		if err != nil {
			return err
		}
	}
	return nil
}

// Connect :
func (c *ChainConnector) Connect(node Node) error {
	err := c.ConnectTo(node)
	if err != nil {
		return err
	}
	c.nodes.Add(node)
	return nil
}

// ConnectTo :
func (c *ChainConnector) ConnectTo(node Node) error {
	last := c.GetLast()
	if last != nil {
		err := last.ConnectTo(node)
		if err != nil {
			return err
		}
	}
	return nil
}

// NewChainConnector :
func NewChainConnector(ctx context.Context, nodes []Node) *ChainConnector {
	ctx, cancel := context.WithCancel(ctx)
	node := &ChainConnector{
		BaseNode: NewBaseNode(ctx, cancel, ""),
		nodes:    doublylinkedlist.New(),
	}
	err := node.ConnectMany(nodes)
	logging.PanicIf(err)
	return node
}

// GroupConnector :
type GroupConnector struct {
	Node
	followers []Node
}

// Start :
func (c *GroupConnector) Start(killChan chan<- error) {
	c.Node.Start(killChan)
	for _, node := range c.followers {
		node.Start(killChan)
	}
}

// Stop :
func (c *GroupConnector) Stop() error {
	for _, node := range c.followers {
		err := node.Stop()
		if err != nil {
			return err
		}
	}
	return c.Node.Stop()
}

// Wait :
func (c *GroupConnector) Wait() error {
	for _, node := range c.followers {
		err := node.Wait()
		if err != nil {
			return err
		}
	}
	return c.Node.Wait()
}

// Join :
func (c *GroupConnector) Join(node Node) {
	c.followers = append(c.followers, node)
}

// NewGroupConnector :
func NewGroupConnector(ctx context.Context, master Node) *GroupConnector {
	return &GroupConnector{
		Node:      master,
		followers: make([]Node, 0),
	}
}
