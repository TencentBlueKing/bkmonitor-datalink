// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package stats

type Stats struct {
	Reload       int
	Version      string
	RunningTasks map[string]int
}

func (s Stats) Copy() Stats {
	newStats := Stats{
		Reload:       s.Reload,
		Version:      s.Version,
		RunningTasks: make(map[string]int),
	}

	for k, v := range s.RunningTasks {
		newStats.RunningTasks[k] = v
	}
	return newStats
}

var stats = &Stats{}

// Default 返回默认 Stats 副本 避免数据被修改
func Default() Stats {
	return stats.Copy()
}

func IncReload() {
	stats.Reload++
}

func SetVersion(v string) {
	stats.Version = v
}

func SetRunningTasks(tasks map[string]int) {
	stats.RunningTasks = tasks
}
