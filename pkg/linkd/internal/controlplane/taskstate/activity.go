// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskstate

import "fmt"

// Activity 接收一个实际循环的存活通知；同一实例重复通知幂等，不覆盖同任务其他执行者。
// 调用方只能在循环真正开始/退出时更新，创建观察器本身不表示任务正在运行。
type Activity struct {
	registry *Registry
	entry    *entry
	active   bool
}

// ObserveActivity 为已注册且启用的任务创建独立观察者，供内部管理 goroutine 的运行器使用。
// 每个观察者的 true 必须在退出路径配对 false；不能用构造成功代替实际运行状态。
func (r *Registry) ObserveActivity(id string) (*Activity, error) {
	if r == nil {
		return nil, fmt.Errorf("task registry is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[id]
	if e == nil || !e.task.Enabled {
		return nil, fmt.Errorf("control plane task %q is not registered or enabled", id)
	}
	return &Activity{registry: r, entry: e}, nil
}

// SetActive 原子应用当前观察者的状态变化；多个循环退出次序不会提前清空其他循环的存活状态。
func (a *Activity) SetActive(active bool) {
	a.registry.mu.Lock()
	defer a.registry.mu.Unlock()
	if a.active == active {
		return
	}
	a.active = active
	if active {
		a.entry.active++
	} else {
		a.entry.active--
	}
}
