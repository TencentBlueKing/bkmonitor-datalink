// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package checker

import (
	"context"
	"testing"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

type noopTask struct {
	tasks.BaseTask
}

func (t *noopTask) GetTaskID() int32 {
	return 0
}

func (t *noopTask) Run(ctx context.Context, event chan<- define.Event) {
	t.PreRun(ctx)
	t.PostRun(ctx)
	event <- nil
}

type mockBeater struct {
	eventChan chan define.Event
}

func (b *mockBeater) Run() error { return nil }

func (b *mockBeater) Stop() {}

func (b *mockBeater) Reload(*common.Config) {}

func (b *mockBeater) GetEventChan() chan define.Event { return b.eventChan }

func (b *mockBeater) GetConfig() define.Config { return nil }

func (b *mockBeater) GetScheduler() define.Scheduler { return nil }

func TestCheckScheduler(t *testing.T) {
	eventChan := make(chan define.Event, 1)
	bt := &mockBeater{
		eventChan: eventChan,
	}
	conf := configs.NewConfig()

	sch := New(bt, conf)

	task := &noopTask{}
	task.TaskConfig = configs.NewTCPTaskConfig()
	task.Init()

	sch.Add(task)
	err := sch.Start(context.Background())
	if err != nil {
		t.Errorf(err.Error())
	}
	sch.Wait()
	sch.Stop()

	_, ok := <-eventChan
	if !ok {
		t.Errorf("get event failed")
	}

	if task.GetStatus() != define.TaskFinished {
		t.Errorf("run task error")
	}
}
