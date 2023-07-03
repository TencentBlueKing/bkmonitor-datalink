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

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// FanOutConnectorSuite :
type FanOutConnectorSuite struct {
	ContextSuite
}

// TestUsage :
func (s *FanOutConnectorSuite) TestUsage() {
	var wg sync.WaitGroup
	ctrl := gomock.NewController(s.T())
	payload := NewMockPayload(ctrl)

	inputCh := make(chan define.Payload)
	input := NewMockNode(ctrl)
	input.EXPECT().GetOutputChan().Return(inputCh)
	input.EXPECT().String().Return("+").AnyTimes()
	input.EXPECT().NoCopy().AnyTimes()

	connector := pipeline.NewFanOutConnector(s.CTX, input)

	outputChs := make([]<-chan define.Payload, 0)
	outputs := make([]pipeline.Node, 0)
	for i := 0; i < 10; i++ {
		output := NewMockNode(ctrl)
		outputs = append(outputs, output)
		output.EXPECT().String().Return(fmt.Sprintf("$%d", i)).AnyTimes()
		output.EXPECT().ConnectFrom(gomock.Any()).DoAndReturn(func(ch <-chan define.Payload) error {
			outputChs = append(outputChs, ch)
			return nil
		})
		s.NoError(connector.ConnectTo(output))
	}

	wg.Add(1)
	go func() {
		inputCh <- payload
		wg.Done()
	}()

	killCh := make(chan error)
	go func() {
		for err := range killCh {
			panic(err)
		}
	}()

	connector.Start(killCh)
	for i, ch := range outputChs {
		p := <-ch
		s.Equalf(p, payload, "index %d", i)
	}
	wg.Wait()
	s.NoError(connector.Stop())
	s.NoError(connector.Wait())

	close(inputCh)
	close(killCh)
	ctrl.Finish()
}

// TestFanOutConnectorSuite :
func TestFanOutConnectorSuite(t *testing.T) {
	suite.Run(t, new(FanOutConnectorSuite))
}

// FanInConnectorSuite :
type FanInConnectorSuite struct {
	ContextSuite
}

// TestUsage :
func (s *FanInConnectorSuite) TestUsage() {
	var wg sync.WaitGroup
	ctrl := gomock.NewController(s.T())

	inputChs := make([]chan define.Payload, 0)
	payloads := make([]define.Payload, 0)
	for i := 0; i < 10; i++ {
		payload := NewMockPayload(ctrl)
		payloads = append(payloads, payload)
		inputChs = append(inputChs, make(chan define.Payload))
	}

	var outputCh <-chan define.Payload
	output := NewMockNode(ctrl)
	output.EXPECT().String().Return("$").AnyTimes()
	output.EXPECT().ConnectFrom(gomock.Any()).DoAndReturn(func(ch <-chan define.Payload) error {
		outputCh = ch
		return nil
	})

	connector := pipeline.NewFanInConnector(s.CTX, output)

	wg.Add(1)
	go func() {
		for i, ch := range inputChs {
			ch <- payloads[i]
		}
		wg.Done()
	}()

	killCh := make(chan error)
	go func() {
		for err := range killCh {
			panic(err)
		}
	}()

	connector.Start(killCh)

	n := 0
	for payload := range outputCh {
		s.Equal(payloads[n], payload)
		n++
	}

	wg.Wait()
	s.NoError(connector.Stop())
	s.NoError(connector.Wait())

	close(killCh)
	ctrl.Finish()
}

// ChainConnectorSuite :
type ChainConnectorSuite struct {
	ContextSuite
}

// TestUsage :
func (s *ChainConnectorSuite) TestUsage() {
	ctrl := gomock.NewController(s.T())
	inputCh := make(<-chan define.Payload)

	chan3 := make(<-chan define.Payload)
	node3 := NewMockNode(ctrl)
	node3.EXPECT().String().AnyTimes()
	node3.EXPECT().GetOutputChan().Return(chan3).AnyTimes()
	node3.EXPECT().Start(gomock.Any())
	node3.EXPECT().Stop().Return(nil)
	node3.EXPECT().Wait().Return(nil)

	chan2 := make(<-chan define.Payload)
	node2 := NewMockNode(ctrl)
	node2.EXPECT().String().AnyTimes()
	node2.EXPECT().GetOutputChan().Return(chan2).AnyTimes()
	node2.EXPECT().Start(gomock.Any())
	node2.EXPECT().Stop().Return(nil)
	node2.EXPECT().Wait().Return(nil)
	node2.EXPECT().ConnectTo(gomock.Any()).DoAndReturn(func(node pipeline.Node) error {
		s.Equal(node3, node)
		return nil
	})

	chan1 := make(<-chan define.Payload)
	node1 := NewMockNode(ctrl)
	node1.EXPECT().String().AnyTimes()
	node1.EXPECT().GetOutputChan().Return(chan1).AnyTimes()
	node1.EXPECT().Start(gomock.Any())
	node1.EXPECT().Stop().Return(nil)
	node1.EXPECT().Wait().Return(nil)
	node1.EXPECT().ConnectTo(gomock.Any()).DoAndReturn(func(node pipeline.Node) error {
		s.Equal(node2, node)
		return nil
	})
	node1.EXPECT().ConnectFrom(gomock.Any()).DoAndReturn(func(ch <-chan define.Payload) error {
		s.Equal(inputCh, ch)
		return nil
	})

	chain := pipeline.NewChainConnector(s.CTX, []pipeline.Node{node1, node2})
	s.NoError(chain.Connect(node3))
	s.NoError(chain.ConnectFrom(inputCh))

	killCh := make(chan error)
	chain.Start(killCh)
	s.NoError(chain.Stop())
	s.NoError(chain.Wait())

	s.Equal(node1, chain.GetFirst())
	s.Equal(node3, chain.GetLast())
	s.Equal(chan3, chain.GetOutputChan())

	ctrl.Finish()
}

// TestChainConnectorSuite :
func TestChainConnectorSuite(t *testing.T) {
	suite.Run(t, new(ChainConnectorSuite))
}
