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
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// FrontendNodeSuite :
type FrontendNodeSuite struct {
	ETLSuite
}

// TestUsage :
func (s *FrontendNodeSuite) TestUsage() {
	frontend := NewMockFrontend(s.Ctrl)
	frontend.EXPECT().Pull(gomock.Any(), gomock.Any()).Return()
	frontend.EXPECT().String().Return("test").AnyTimes()
	frontend.EXPECT().Close().Return(nil)
	killCh := make(chan error)

	n := pipeline.NewFrontendNode(s.CTX, s.Cancel, frontend, time.Second)
	n.Start(killCh)
	s.NoError(n.Stop())
	s.NoError(n.Wait())
}

// TestCleanUp :
func (s *FrontendNodeSuite) TestCleanUp() {
	frontend := NewMockFrontend(s.Ctrl)
	i := 0
	frontend.EXPECT().Pull(gomock.Any(), gomock.Any()).DoAndReturn(func(outputChan chan<- define.Payload, killChan chan<- error) {
	loop:
		for {
			payload := NewMockPayload(s.Ctrl)
			select {
			case outputChan <- payload:
				i++
			case <-s.CTX.Done():
				break loop
			}
		}
	})
	frontend.EXPECT().String().Return("test").AnyTimes()
	frontend.EXPECT().Close().Return(nil)

	killCh := make(chan error)
	n := pipeline.NewFrontendNode(s.CTX, s.Cancel, frontend, time.Second)
	n.Start(killCh)
	outCh := n.GetOutputChan()
	<-outCh
	s.NoError(n.Stop())
	s.Cancel()
	s.NoError(n.Wait())
	s.True(i >= 1)
}

// TestFrontendNodeSuite :
func TestFrontendNodeSuite(t *testing.T) {
	suite.Run(t, new(FrontendNodeSuite))
}

// BackendNodeSuite :
type BackendNodeSuite struct {
	ContextSuite
}

// TestUsage :
func (s *BackendNodeSuite) TestUsage() {
	ctx := s.CTX
	ctrl := gomock.NewController(s.T())
	backend := NewMockBackend(ctrl)
	backend.EXPECT().Push(gomock.Any(), gomock.Any()).Return()
	backend.EXPECT().String().Return("test").AnyTimes()
	backend.EXPECT().Close().Return(nil)
	killCh := make(chan error)
	inputCh := make(chan define.Payload)

	n := pipeline.NewBackendNode(ctx, s.Cancel, backend)
	s.NoError(n.ConnectFrom(inputCh))
	n.Start(killCh)
	inputCh <- NewMockPayload(ctrl)
	s.NoError(n.Stop())
	s.NoError(n.Wait())
	ctrl.Finish()
}

// TestBackendNodeSuite :
func TestBackendNodeSuite(t *testing.T) {
	suite.Run(t, new(BackendNodeSuite))
}

// ProcessNodeSuite :
type ProcessNodeSuite struct {
	ContextSuite
}

// TestUsage :
func (s *ProcessNodeSuite) TestUsage() {
	ctx := s.CTX
	ctrl := gomock.NewController(s.T())
	processor := NewMockDataProcessor(ctrl)
	processor.EXPECT().Process(gomock.Any(), gomock.Any(), gomock.Any()).Return()
	processor.EXPECT().Finish(gomock.Any(), gomock.Any()).AnyTimes()
	processor.EXPECT().String().Return("test").AnyTimes()
	killCh := make(chan error)
	inputCh := make(chan define.Payload)

	cfg := new(config.PipelineConfig)
	cfg.ETLConfig = "test"
	subctx := config.PipelineConfigIntoContext(ctx, cfg)

	n := pipeline.NewProcessNode(subctx, s.Cancel, processor)
	s.NoError(n.ConnectFrom(inputCh))
	n.Start(killCh)
	inputCh <- NewMockPayload(ctrl)
	s.NoError(n.Stop())
	s.NoError(n.Wait())

	ctrl.Finish()
}

// TestProcessNodeSuite :
func TestProcessNodeSuite(t *testing.T) {
	suite.Run(t, new(ProcessNodeSuite))
}
