// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// BaseBuilderSuite
type BaseBuilderSuite struct {
	ConfigSuite
}

// CreateSimpleMockNode
func (s *BaseBuilderSuite) CreateSimpleMockNode(name string) *MockNode {
	node := NewMockNode(s.Ctrl)
	node.EXPECT().String().Return(name).AnyTimes()
	return node
}

// ExpectMockNodeRun
func (s *BaseBuilderSuite) ExpectMockNodeRun(node *MockNode) {
	node.EXPECT().Start(gomock.Any()).AnyTimes()
	node.EXPECT().Stop().Return(nil).AnyTimes()
	node.EXPECT().Wait().Return(nil).AnyTimes()
}

// ExpectMockNodeConnected
func (s *BaseBuilderSuite) ExpectMockNodeConnected(node *MockNode, ch chan define.Payload) {
	node.EXPECT().ConnectTo(gomock.Any()).AnyTimes()
	node.EXPECT().ConnectFrom(gomock.Any()).AnyTimes()
	node.EXPECT().GetOutputChan().Return(ch).AnyTimes()
}

// CreateFrontendMockNode
func (s *BaseBuilderSuite) CreateFrontendMockNode(ch chan define.Payload) *MockNode {
	node := s.CreateSimpleMockNode("frontend")
	s.ExpectMockNodeConnected(node, ch)
	node.EXPECT().NoCopy().AnyTimes()
	return node
}

// CreateNamedProcessorMockNode
func (s *BaseBuilderSuite) CreateNamedProcessorMockNode(name string, ch chan define.Payload) *MockNode {
	node := s.CreateSimpleMockNode(name)
	s.ExpectMockNodeConnected(node, ch)
	return node
}

// CreateProcessorMockNode
func (s *BaseBuilderSuite) CreateProcessorMockNode(ch chan define.Payload) *MockNode {
	return s.CreateNamedProcessorMockNode("processor", ch)
}

// CreateBackendMockNode
func (s *BaseBuilderSuite) CreateBackendMockNode() *MockNode {
	node := s.CreateSimpleMockNode("backend")
	s.ExpectMockNodeConnected(node, nil)
	return node
}

// CreateMockFrontend
func (s *BaseBuilderSuite) CreateMockFrontend(name string) *MockFrontend {
	frontend := NewMockFrontend(s.Ctrl)
	frontend.EXPECT().Pull(gomock.Any(), gomock.Any()).Return().AnyTimes()
	frontend.EXPECT().Commit().AnyTimes()
	frontend.EXPECT().Reset().AnyTimes()
	frontend.EXPECT().Close().Return(nil).AnyTimes()
	frontend.EXPECT().String().Return(name).AnyTimes()
	return frontend
}

// CreateMockProcessor
func (s *BaseBuilderSuite) CreateMockProcessor(name string) *MockDataProcessor {
	processor := NewMockDataProcessor(s.Ctrl)
	processor.EXPECT().Process(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	processor.EXPECT().Finish(gomock.Any(), gomock.Any()).AnyTimes()
	processor.EXPECT().String().Return(name).AnyTimes()
	return processor
}

// CreateMockBackend
func (s *BaseBuilderSuite) CreateMockBackend(name string) *MockBackend {
	backend := NewMockBackend(s.Ctrl)
	backend.EXPECT().Close().Return(nil).AnyTimes()
	backend.EXPECT().String().Return(name).AnyTimes()
	return backend
}

// BuilderSuite
type BuilderSuite struct {
	BaseBuilderSuite
}

// TestEdgesLeak :
func (s *BuilderSuite) TestEdgesLeak() {
	ctx := s.CTX

	fNode := s.CreateFrontendMockNode(nil)
	pNode1 := s.CreateNamedProcessorMockNode("processor1", nil)
	pNode2 := s.CreateNamedProcessorMockNode("processor2", nil)
	bNode := s.CreateBackendMockNode()

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		Connect(fNode, pNode1).Connect(pNode2, bNode).
		Finish()
	s.Error(err)
	s.Nil(pipe)
}

// TestBranching :
func (s *BuilderSuite) TestBranching() {
	ctx := s.CTX

	fNode := s.CreateFrontendMockNode(nil)

	pipe := pipeline.NewBuilderWithFrontend(ctx, fNode, "test")

	for i := 0; i <= 10; i++ {
		processor := s.CreateMockProcessor(fmt.Sprintf("processor-%d", i))
		pNode := pipeline.NewProcessNode(s.CTX, s.Cancel, processor)

		formatter := s.CreateMockProcessor(fmt.Sprintf("formatter-%d", i))
		fmtNode := pipeline.NewProcessNode(s.CTX, s.Cancel, formatter)

		backend := s.CreateMockBackend(fmt.Sprintf("backend-%d", i))
		bNode := pipeline.NewBackendNode(s.CTX, s.Cancel, backend)

		pipe.ConnectFrontend(pNode).Connect(pNode, fmtNode).Connect(fmtNode, bNode)
	}

	p, err := pipe.Finish()
	s.NoError(err)
	s.NotNil(p)
}

// TestBuildEmptyPipeline :
func (s *BuilderSuite) TestBuildEmptyPipeline() {
	pipe, err := pipeline.NewBuilder(s.CTX, "test").Finish()
	s.Error(err)
	s.Nil(pipe)
}

// TestBranchingRun :
func (s *BuilderSuite) TestBranchingRun() {
	chans := make([]<-chan define.Payload, 7)
	var wg sync.WaitGroup

	processors := make([]pipeline.Node, 0)
	for i := 0; i < 7; i++ {
		wg.Add(1)
		p := NewMockNode(s.Ctrl)
		func(index int, processor *MockNode) {
			var outCh chan define.Payload

			processor.EXPECT().Stop()
			if i == 0 || i == 3 {
				processor.EXPECT().NoCopy()
			}

			processor.EXPECT().Wait()
			processor.EXPECT().String().Return(fmt.Sprintf("p%d", index)).AnyTimes()
			processor.EXPECT().GetOutputChan().DoAndReturn(func() <-chan define.Payload {
				logging.Debugf("%d %v make output channel", index, processor)
				if outCh == nil {
					outCh = make(chan define.Payload)
				}
				return outCh
			}).AnyTimes()
			processor.EXPECT().ConnectTo(gomock.Any()).AnyTimes().DoAndReturn(func(to pipeline.Node) error {
				logging.Debugf("%d %v connect to %v", index, processor, to)
				return to.ConnectFrom(processor.GetOutputChan())
			})
			processor.EXPECT().ConnectFrom(gomock.Any()).AnyTimes().DoAndReturn(func(ch <-chan define.Payload) error {
				logging.Debugf("%v connected", processor)
				chans[index] = ch
				return nil
			})
			processor.EXPECT().Start(gomock.Any()).DoAndReturn(func(killChan chan<- error) {
				go func() {
					ch := chans[index]
					if ch == nil {
						panic(fmt.Errorf("%v input channel is empty", processor))
					}

					logging.Debugf("%v waiting", processor)
					payload := <-ch
					logging.Debugf("%v received", processor)

					if outCh != nil {
						logging.Debugf("%v pushing", processor)
						outCh <- payload
						logging.Debugf("%v pushed", processor)
					}
					wg.Done()
				}()
			})
		}(i, p)
		processors = append(processors, p)
	}

	// p0 --> p1
	// p0 --> p2
	// p1 --> p3
	// p2 --> p4
	// p3 --> p5
	// p3 --> p6

	builder := pipeline.NewBuilderWithFrontend(s.CTX, processors[0], "test")
	pipe, err := builder.
		Connect(processors[0], processors[1]).
		Connect(processors[0], processors[2]).
		Connect(processors[1], processors[3]).
		Connect(processors[2], processors[4]).
		Connect(processors[3], processors[5]).
		Connect(processors[3], processors[6]).
		Finish()

	s.NoError(err)
	s.NotNil(pipe)

	inputCh := make(chan define.Payload)
	chans[0] = inputCh

	pipe.Start()
	inputCh <- NewMockPayload(s.Ctrl)
	wg.Wait()
	s.NoError(pipe.Stop(0))
	s.NoError(pipe.Wait())
}

// TestPassByConnectLoop :
func (s *BuilderSuite) TestPassByConnectLoop() {
	ctx := s.CTX

	fNode := s.CreateFrontendMockNode(nil)
	bNode := s.CreateBackendMockNode()

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		Connect(fNode, bNode).Connect(bNode, fNode).
		Finish()
	s.Error(err)
	s.Nil(pipe)
}

// TestFanOutConnectLoop :
func (s *BuilderSuite) TestFanOutConnectLoop() {
	ctx := s.CTX

	fNode := s.CreateFrontendMockNode(nil)
	pNode1 := s.CreateNamedProcessorMockNode("processor1", nil)
	pNode2 := s.CreateNamedProcessorMockNode("processor2", nil)

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		Connect(fNode, pNode1).Connect(fNode, pNode2).Connect(pNode2, fNode).
		Finish()
	s.Error(err)
	s.Nil(pipe)
}

// TestConnectLoop1 :
func (s *BuilderSuite) TestConnectLoop1() {
	ctx := s.CTX

	fNode := s.CreateFrontendMockNode(nil)
	pNode1 := s.CreateNamedProcessorMockNode("processor1", nil)
	pNode2 := s.CreateNamedProcessorMockNode("processor2", nil)

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		Connect(fNode, pNode1).Connect(fNode, pNode2).Connect(pNode2, pNode1).Connect(pNode1, pNode2).
		Finish()
	s.Error(err)
	s.Nil(pipe)
}

// TestConnectLoop2 :
func (s *BuilderSuite) TestConnectLoop2() {
	ctx := s.CTX

	fNode := s.CreateFrontendMockNode(nil)
	pNode1 := s.CreateNamedProcessorMockNode("processor1", nil)
	pNode2 := s.CreateNamedProcessorMockNode("processor2", nil)
	pNode3 := s.CreateNamedProcessorMockNode("processor3", nil)

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		Connect(fNode, pNode1).Connect(pNode1, pNode2).Connect(pNode2, pNode3).Connect(pNode3, pNode1).
		Finish()
	s.Error(err)
	s.Nil(pipe)
}

// TestConnectLoop3 :
func (s *BuilderSuite) TestConnectLoop3() {
	ctx := s.CTX

	fNode := s.CreateFrontendMockNode(nil)
	pNode1 := s.CreateNamedProcessorMockNode("processor1", nil)
	pNode2 := pNode1

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		ConnectFrontend(pNode1).Connect(pNode1, pNode2).
		Finish()
	s.Error(err)
	s.Nil(pipe)
}

// TestUsage :
func (s *BuilderSuite) TestUsage() {
	var wg1, wg2 sync.WaitGroup
	ctx := s.CTX
	ctrl := gomock.NewController(s.T())

	payload := NewMockPayload(ctrl)

	wg1.Add(1)
	frontend := NewMockFrontend(ctrl)
	frontend.EXPECT().String().Return("frontend").AnyTimes()
	fNode := pipeline.NewFrontendNode(s.CTX, s.Cancel, frontend, time.Second)
	frontend.EXPECT().Pull(gomock.Any(), gomock.Any()).DoAndReturn(func(outputCh chan<- define.Payload, killCh chan<- error) {
		outputCh <- payload
		wg1.Done()
	})
	frontend.EXPECT().Close().Return(nil)

	wg1.Add(1)
	processor := NewMockDataProcessor(ctrl)
	processor.EXPECT().String().Return("processor").AnyTimes()
	pNode := pipeline.NewProcessNode(s.CTX, s.Cancel, processor)
	processor.EXPECT().Finish(gomock.Any(), gomock.Any())
	processor.EXPECT().Process(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(p define.Payload, outputCh chan<- define.Payload, killCh chan<- error) {
		s.Equal(payload, p)
		outputCh <- p
		wg1.Done()
	})

	wg1.Add(1)
	backend := NewMockBackend(ctrl)
	backend.EXPECT().String().Return("backend").AnyTimes()
	backend.EXPECT().Close().Return(nil)
	bNode := pipeline.NewBackendNode(s.CTX, s.Cancel, backend)
	backend.EXPECT().Push(gomock.Any(), gomock.Any()).DoAndReturn(func(p define.Payload, killCh chan<- error) {
		s.Equal(payload, p)
		wg1.Done()
	})

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		Connect(fNode, pNode).Connect(pNode, bNode).
		Finish()
	s.NoError(err)

	killCh := pipe.Start()
	wg2.Add(1)
	go func() {
		for err := range killCh {
			panic(err)
		}
		wg2.Done()
	}()

	wg1.Wait()
	s.NoError(pipe.Stop(0))
	s.NoError(pipe.Wait())
	wg2.Wait()
	ctrl.Finish()
}

// TestHeadAndTails
func (s *BuilderSuite) TestHeadAndTails() {
	ctx := s.CTX

	fNode := s.CreateFrontendMockNode(nil)
	s.ExpectMockNodeConnected(fNode, nil)
	pNode := s.CreateSimpleMockNode("processor1")
	s.ExpectMockNodeConnected(pNode, nil)
	bNode := s.CreateBackendMockNode()
	s.ExpectMockNodeConnected(bNode, nil)

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		ConnectFrontend(pNode).Connect(pNode, bNode).
		Finish()
	s.NoError(err)
	s.NotNil(pipe)
	s.Equal(fNode.String(), pipe.Head().String())
	s.Equal(2, len(pipe.Tails()))
}

// TestTerminate
func (s *BuilderSuite) TestTerminate() {
	ctx := s.CTX
	done := 0
	var wg sync.WaitGroup
	output := make(chan define.Payload)

	wg.Add(1)
	fNode := s.CreateFrontendMockNode(output)
	s.ExpectMockNodeConnected(fNode, output)
	fNode.EXPECT().Start(gomock.Any()).DoAndReturn(func(killCh chan<- error) {
		wg.Done()
	})
	fNode.EXPECT().Stop().DoAndReturn(func() error {
		s.Equal(0, done)
		done++
		return nil
	})

	wg.Add(1)
	pNode := s.CreateProcessorMockNode(output)
	s.ExpectMockNodeConnected(pNode, output)
	pNode.EXPECT().Start(gomock.Any()).DoAndReturn(func(killCh chan<- error) {
		wg.Done()
	})
	pNode.EXPECT().Stop().DoAndReturn(func() error {
		done++
		return nil
	})

	wg.Add(1)
	bNode := s.CreateBackendMockNode()
	s.ExpectMockNodeConnected(bNode, output)
	bNode.EXPECT().Start(gomock.Any()).DoAndReturn(func(killCh chan<- error) {
		wg.Done()
	})
	bNode.EXPECT().Stop().DoAndReturn(func() error {
		done++
		return nil
	})

	pipe, err := pipeline.NewBuilderWithFrontend(ctx, fNode, "test").
		ConnectFrontend(pNode).Connect(pNode, bNode).
		Finish()
	pipe.Start()
	wg.Wait()
	s.NoError(pipe.Stop(time.Millisecond))
	s.NoError(err)
	s.NotNil(pipe)
	s.Equal(3, done)
}

// ConfigBuilderSuite
type ConfigBuilderSuite struct {
	BaseBuilderSuite
}

// TestBuilderSuite :
func TestBuilderSuite(t *testing.T) {
	suite.Run(t, new(BuilderSuite))
}

// TestTestConnectDataProcessors
func TestTestConnectDataProcessors(t *testing.T) {
	suite.Run(t, new(ConfigBuilderSuite))
}
