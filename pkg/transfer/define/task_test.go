// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// TaskManagerSuite :
type TaskManagerSuite struct {
	ContextSuite
}

// TestUsage :
func (s *TaskManagerSuite) TestUsage() {
	task := NewMockTask(s.Ctrl)
	task.EXPECT().Start().Return(nil)
	task.EXPECT().Stop().Return(nil)
	task.EXPECT().Wait().Return(nil)

	taskManager := define.NewTaskManager()
	taskManager.Add(task)
	count := 0
	s.NoError(taskManager.ForEach(func(index int, t define.Task) error {
		s.Equal(task, t)
		count++
		return nil
	}))
	s.Equal(1, count)

	s.NoError(taskManager.Start())
	s.NoError(taskManager.Stop())
	s.NoError(taskManager.Wait())

	taskManager.Clear()
	count = 0
	s.NoError(taskManager.ForEach(func(index int, t define.Task) error {
		count++
		return nil
	}))
	s.Equal(0, count)
}

// TestTaskManagerSuite :
func TestTaskManagerSuite(t *testing.T) {
	suite.Run(t, new(TaskManagerSuite))
}
