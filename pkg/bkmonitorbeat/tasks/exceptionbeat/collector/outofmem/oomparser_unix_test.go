// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || dragonfly || linux || netbsd || openbsd || solaris || zos

package outofmem

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetContainerName(t *testing.T) {
	line := "oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=collector-ttt6,mems_allowed=0,oom_memcg=/collector-ttt6,task_memcg=/collector-ttt6,task=ttt6,pid=27530,uid=0"
	oomCurrentInstance := &OomInstance{
		ContainerName:       "/",
		VictimContainerName: "/",
		TimeOfDeath:         time.Now(),
	}

	ok, err := getContainerName(line, oomCurrentInstance)
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "ttt6", oomCurrentInstance.ProcessName)
	assert.Equal(t, "CONSTRAINT_MEMCG", oomCurrentInstance.Constraint)
	assert.Equal(t, "/collector-ttt6", oomCurrentInstance.ContainerName)
	assert.Equal(t, "/collector-ttt6", oomCurrentInstance.VictimContainerName)
}
