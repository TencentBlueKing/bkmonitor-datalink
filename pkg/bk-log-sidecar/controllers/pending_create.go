// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controllers

import "time"

type pendingContainerCreation struct {
	sequence uint64
	deadline time.Time
}

func (s *BkLogSidecar) recordPendingContainerCreate(
	containerID string,
	sequence uint64,
	deadline time.Time,
) {
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()
	if s.pendingContainerCreates == nil {
		s.pendingContainerCreates = make(map[string]pendingContainerCreation)
	}
	s.pendingContainerCreates[containerID] = pendingContainerCreation{
		sequence: sequence,
		deadline: deadline,
	}
	// pending CREATE 会改变周期全量的 tail_files 决策，因此必须让
	// 已在途的 Build 失效，避免旧快照先落盘 tail_files=true。
	s.configGeneration++
}

// clearPendingContainerCreate 仅清理当前 CREATE 的标记。sequence=0 用于
// STOP/DELETE 无条件终止尚未完成的 CREATE 稳定窗口。
func (s *BkLogSidecar) clearPendingContainerCreate(containerID string, sequence uint64) {
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()
	pending, ok := s.pendingContainerCreates[containerID]
	if !ok || (sequence != 0 && pending.sequence != sequence) {
		return
	}
	delete(s.pendingContainerCreates, containerID)
	s.configGeneration++
}

func (s *BkLogSidecar) isPendingContainerCreate(containerID string) bool {
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()
	pending, ok := s.pendingContainerCreates[containerID]
	return ok && time.Now().Before(pending.deadline)
}

func (s *BkLogSidecar) containerCreateVisibilityInterval() time.Duration {
	if s.createEventVisibilityWindow > 0 {
		return s.createEventVisibilityWindow
	}
	return CreateEventVisibilityWindow
}
