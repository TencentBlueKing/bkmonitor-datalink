// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// SchedulerSuite :
type SchedulerSuite struct {
	StoreSuite
	pipeline    *MockPipeline
	scheduler   *scheduler.Scheduler
	watcherCh   chan *define.WatchEvent
	newPipeline func(ctx context.Context, name string) (define.Pipeline, error)
	newStore    func(ctx context.Context, name string) (define.Store, error)
}

// SetupTest :
func (s *SchedulerSuite) SetupTest() {
	var err error
	s.StoreSuite.SetupTest()
	s.newPipeline = define.NewPipeline
	s.newStore = define.NewStore

	define.NewStore = func(ctx context.Context, name string) (store define.Store, e error) {
		return s.Store, nil
	}

	s.pipeline = NewMockPipeline(s.Ctrl)
	s.pipeline.EXPECT().String().Return("test").AnyTimes()
	define.NewPipeline = func(ctx context.Context, name string) (define.Pipeline, error) {
		return s.pipeline, nil
	}

	s.Config.Set(scheduler.ConfSchedulerCheckIntervalKey, "10ms")
	s.Config.Set(scheduler.ConfSchedulerCleanUpDurationKey, "1s")

	s.watcherCh = make(chan *define.WatchEvent)
	s.scheduler, err = scheduler.NewScheduler(s.CTX, "test", func(ctx context.Context) <-chan *define.WatchEvent {
		return s.watcherCh
	})
	s.NoError(err)
	s.scheduler.TaskManager.Clear()
	s.PipelineConfig.DataID = 111
}

// TearDownTest :
func (s *SchedulerSuite) TearDownTest() {
	define.NewPipeline = s.newPipeline
	define.NewStore = s.newStore

	s.StoreSuite.TearDownTest()
}

// TestCheckPipelineConfig :
func (s *SchedulerSuite) TestCheckPipelineConfig() {
	s.NoError(s.scheduler.CheckPipelineConfig(s.PipelineConfig))
}

// TestActivatePipeline :
func (s *SchedulerSuite) TestActivatePipeline() {
	s.pipeline.EXPECT().Start().Return(make(chan error))

	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
}

// TestActivatePipeline :
func (s *SchedulerSuite) TestActivatePipelineTwice() {
	s.pipeline.EXPECT().Start().Return(make(chan error))

	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))

	s.Errorf(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig), "pipeline %d already exists", s.PipelineConfig.DataID)
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
}

// TestActivatePipelineWithPanic :
func (s *SchedulerSuite) TestActivatePipelineWithPanic() {
	s.pipeline.EXPECT().Start().DoAndReturn(func() <-chan error {
		panic(fmt.Errorf("test"))
	})
	s.Errorf(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig), "test")
	s.NotNil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
}

// TestDeactivatePipeline :
func (s *SchedulerSuite) TestDeactivatePipeline() {
	s.pipeline.EXPECT().Start().Return(make(chan error))
	s.pipeline.EXPECT().Stop(gomock.Any()).Return(nil)
	s.pipeline.EXPECT().Wait().Return(nil)
	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.NoError(s.scheduler.PipelineManager.Deactivate(s.PipelineConfig.DataID))
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
}

// TestDeactivatePipeline :
func (s *SchedulerSuite) TestDeactivatePipelineTwice() {
	s.pipeline.EXPECT().Start().Return(make(chan error))
	s.pipeline.EXPECT().Stop(gomock.Any()).Return(nil)
	s.pipeline.EXPECT().Wait().Return(nil)

	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))

	s.NoError(s.scheduler.PipelineManager.Deactivate(s.PipelineConfig.DataID))
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))

	s.Errorf(s.scheduler.PipelineManager.Deactivate(s.PipelineConfig.DataID), "pipeline %d not found", s.PipelineConfig.DataID)
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
}

// TestDeactivatePipelineWithoutAdd :
func (s *SchedulerSuite) TestDeactivatePipelineWithoutAdd() {
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.Errorf(s.scheduler.PipelineManager.Deactivate(s.PipelineConfig.DataID), "pipeline %v not found", s.PipelineConfig.DataID)
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
}

// TestDeactivatePipelineStopError :
func (s *SchedulerSuite) TestDeactivatePipelineStopError() {
	s.pipeline.EXPECT().Start().Return(make(chan error))
	s.pipeline.EXPECT().Stop(gomock.Any()).Return(fmt.Errorf("test"))
	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Errorf(s.scheduler.PipelineManager.Deactivate(s.PipelineConfig.DataID), "test")
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
}

// TestDeactivatePipelineStopPanic :
func (s *SchedulerSuite) TestDeactivatePipelineStopPanic() {
	s.pipeline.EXPECT().Start().Return(make(chan error))
	s.pipeline.EXPECT().Stop(gomock.Any()).DoAndReturn(func(timeout time.Duration) error {
		panic(fmt.Errorf("test"))
	})
	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Errorf(s.scheduler.PipelineManager.Deactivate(s.PipelineConfig.DataID), "test")
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
}

// TestDeactivatePipelineWaitError :
func (s *SchedulerSuite) TestDeactivatePipelineWaitError() {
	s.pipeline.EXPECT().Start().Return(make(chan error))
	s.pipeline.EXPECT().Stop(gomock.Any()).Return(nil)
	s.pipeline.EXPECT().Wait().Return(fmt.Errorf("test"))
	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Errorf(s.scheduler.PipelineManager.Deactivate(s.PipelineConfig.DataID), "test")
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
}

// TestDeactivatePipelineWaitPanic :
func (s *SchedulerSuite) TestDeactivatePipelineWaitPanic() {
	s.pipeline.EXPECT().Start().Return(make(chan error))
	s.pipeline.EXPECT().Stop(gomock.Any()).Return(nil)
	s.pipeline.EXPECT().Wait().DoAndReturn(func() error {
		panic(fmt.Errorf("test"))
	})
	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Errorf(s.scheduler.PipelineManager.Deactivate(s.PipelineConfig.DataID), "test")
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
}

// TestReactivatePipeline :
func (s *SchedulerSuite) TestReactivatePipeline() {
	s.pipeline.EXPECT().Start().Return(make(chan error)).Times(2)
	s.pipeline.EXPECT().Stop(gomock.Any()).Return(nil)
	s.pipeline.EXPECT().Wait().Return(nil)

	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))

	s.NoError(s.scheduler.PipelineManager.Reactivate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
}

// TestReactivatePipelineWithoutStart :
func (s *SchedulerSuite) TestReactivatePipelineWithoutStart() {
	s.pipeline.EXPECT().Start().Return(make(chan error))

	s.Nil(s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.False(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))

	s.NoError(s.scheduler.PipelineManager.Reactivate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
}

// TestReactivatePipelineTwice :
func (s *SchedulerSuite) TestReactivatePipelineTwice() {
	s.pipeline.EXPECT().Start().Return(make(chan error)).Times(3)
	s.pipeline.EXPECT().Stop(gomock.Any()).Return(nil).Times(2)
	s.pipeline.EXPECT().Wait().Return(nil).Times(2)

	s.NoError(s.scheduler.PipelineManager.Activate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))

	s.NoError(s.scheduler.PipelineManager.Reactivate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))

	s.NoError(s.scheduler.PipelineManager.Reactivate(s.CTX, s.PipelineConfig))
	s.Equal(s.pipeline, s.scheduler.PipelineManager.GetPipeline(s.PipelineConfig.DataID))
	s.True(s.scheduler.PipelineManager.IsAlive(s.PipelineConfig.DataID))
}

// TestSchedulerSuite :
func TestSchedulerSuite(t *testing.T) {
	suite.Run(t, new(SchedulerSuite))
}
