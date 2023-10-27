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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// Pipeline : 管理多个节点的流水线
type Pipeline struct {
	name     string
	ctx      context.Context
	cancelFn context.CancelFunc
	killCh   chan error
	nodes    []Node
}

// String :
func (p *Pipeline) String() string {
	return p.name
}

func (p *Pipeline) Flow() int {
	return p.Head().(*FrontendNode).frontend.Flow()
}

// Head : return frontend node
func (p *Pipeline) Head() Node {
	if p.nodes == nil || len(p.nodes) == 0 {
		return nil
	}
	return p.nodes[0]
}

// Tails : return
func (p *Pipeline) Tails() []Node {
	if p.nodes == nil || len(p.nodes) == 0 {
		return nil
	}
	return p.nodes[1:]
}

// ForEachNode :
func (p *Pipeline) ForEachNode(fn func(k interface{}, n Node) error) error {
	for i, node := range p.nodes {
		err := fn(i, node)
		if err != nil {
			return err
		}
	}
	return nil
}

// Start :
func (p *Pipeline) Start() <-chan error {
	killCh := make(chan error)
	err := p.ForEachNode(func(k interface{}, n Node) error {
		n.Start(killCh) //
		return nil
	})
	logging.PanicIf(err)

	p.killCh = killCh
	return killCh
}

// Stop :
func (p *Pipeline) Stop(timeout time.Duration) error {
	if timeout == 0 {
		return p.ForEachNode(func(k interface{}, n Node) error {
			return n.Stop()
		})
	}
	head := p.Head()
	err := head.Stop()
	utils.TimeoutOrContextDone(p.ctx, time.After(timeout))
	for _, node := range p.Tails() {
		err := node.Stop()
		if err != nil {
			logging.Errorf("pipeline %v stop node %v failed: %v", p, node, err)
		}
	}

	return err
}

// Wait :
func (p *Pipeline) Wait() error {
	p.cancelFn()
	close(p.killCh)
	return p.ForEachNode(func(k interface{}, n Node) error {
		return n.Wait()
	})
}

// NewPipeline :
func NewPipeline(ctx context.Context, name string, nodes []Node) *Pipeline {
	if nodes == nil {
		nodes = make([]Node, 0)
	}
	ctx, cancelFn := context.WithCancel(ctx)
	pipe := &Pipeline{
		name:     name,
		ctx:      ctx,
		cancelFn: cancelFn,
		nodes:    nodes,
	}
	return pipe
}
