// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"fmt"
)

const (
	StatefulSetTaskSecretPrefix = "statefulset-worker"
	DaemonSetTaskSecretPrefix   = "daemonset-worker"
	EventTaskSecretPrefix       = "event-worker"
)

const (
	LabelTaskType = "taskType"

	TaskTypeDaemonSet   = "daemonset"
	TaskTypeEvent       = "event"
	TaskTypeStatefulSet = "statefulset"
)

func ValidateTaskType(t string) bool {
	switch t {
	case TaskTypeDaemonSet, TaskTypeEvent, TaskTypeStatefulSet:
		return true
	}
	return false
}

func GetDaemonSetTaskSecretName(s string) string {
	return fmt.Sprintf("%s-%s", DaemonSetTaskSecretPrefix, s)
}

func GetStatefulSetTaskSecretName(i int) string {
	return fmt.Sprintf("%s-%d", StatefulSetTaskSecretPrefix, i)
}

func GetEventTaskSecretName() string {
	return fmt.Sprintf("%s-0", EventTaskSecretPrefix)
}

func GetTaskLabelSelector(s string) string {
	return fmt.Sprintf("%s=%s", LabelTaskType, s)
}
