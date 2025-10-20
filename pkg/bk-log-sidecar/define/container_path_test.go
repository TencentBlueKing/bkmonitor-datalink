// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainerActualPath(t *testing.T) {
	container := &Container{
		RootPath: "/var/host",
		Mounts: []Mount{
			{
				"/var/logs",
				"/data/logs",
			},
			{
				"/data/container",
				"/data",
			},
			{
				"/tmp",
				"/data/logs/expired",
			},
			{
				"/log/ds",
				"/home/user00/log/ds",
			},
		},
	}

	var path string

	path, _ = ContainerActualPath("/data/a.log", container)
	assert.Equal(t, "/data/container/a.log", path)

	path, _ = ContainerActualPath("/data/logs/xxx/yyy.log", container)
	assert.Equal(t, "/var/logs/xxx/yyy.log", path)

	path, _ = ContainerActualPath("/data/logs/expired/yyy.log", container)
	assert.Equal(t, "/tmp/yyy.log", path)

	path, _ = ContainerActualPath("/root/logs/yyy.log", container)
	assert.Equal(t, "/var/host/root/logs/yyy.log", path)

	path, _ = ContainerActualPath("/home/user00/log/dsa/yyy.log", container)
	assert.Equal(t, "/var/host/home/user00/log/dsa/yyy.log", path)

	path, _ = ContainerActualPath("/home/user00/log/ds/..yyy.log", container)
	assert.Equal(t, "/log/ds/..yyy.log", path)
}
