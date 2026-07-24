// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controllers

import (
	"context"
	"fmt"

	"k8s.io/client-go/util/workqueue"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

const containerEventQueueName = "bk-log-sidecar-container-events"

type containerWorkKind string

const (
	containerWorkEvent          containerWorkKind = "event"
	containerWorkPendingCleanup containerWorkKind = "pending-cleanup"
)

// containerWorkItem 使用值类型放入 workqueue，确保同一重试项可以被正确限速和 Forget。
// sequence 只用于容器事件；pending cleanup 由宽限期 generation 判断是否已经过期。
type containerWorkItem struct {
	kind              containerWorkKind
	containerID       string
	eventType         define.ContainerEventType
	sequence          uint64
	pendingGeneration uint64
}

func (s *BkLogSidecar) getOrCreateContainerEventQueue() workqueue.RateLimitingInterface {
	s.eventQueueMu.Lock()
	defer s.eventQueueMu.Unlock()
	if s.eventQueue == nil {
		s.eventQueue = workqueue.NewNamedRateLimitingQueue(
			workqueue.DefaultControllerRateLimiter(),
			containerEventQueueName,
		)
	}
	return s.eventQueue
}

func (s *BkLogSidecar) startContainerEventWorker(ctx context.Context) {
	queue := s.getOrCreateContainerEventQueue()
	s.eventWorkerOnce.Do(func() {
		s.lifecycleWG.Add(1)
		go func() {
			defer s.lifecycleWG.Done()
			for s.processNextContainerWorkItem(ctx, queue) {
			}
		}()
	})
}

func (s *BkLogSidecar) shutdownContainerEventQueue() {
	s.eventShutdownOnce.Do(func() {
		s.eventQueueMu.Lock()
		defer s.eventQueueMu.Unlock()
		if s.eventQueue != nil {
			s.eventQueue.ShutDownWithDrain()
			// delaying queue 的 ShutDownWithDrain 只关闭底层队列；再调用一次
			// ShutDown，确保延迟重试的 timer/stopCh 也被释放。
			s.eventQueue.ShutDown()
		}
	})
}

func (s *BkLogSidecar) enqueueContainerEvent(event *define.ContainerEvent) {
	if event == nil || event.ContainerID == "" {
		s.log.Info("ignore invalid empty container event")
		return
	}

	s.eventSequenceMu.Lock()
	s.eventSequence++
	sequence := s.eventSequence
	if s.latestEventSequence == nil {
		s.latestEventSequence = make(map[string]uint64)
	}
	s.latestEventSequence[event.ContainerID] = sequence
	s.eventSequenceMu.Unlock()

	s.getOrCreateContainerEventQueue().Add(containerWorkItem{
		kind:        containerWorkEvent,
		containerID: event.ContainerID,
		eventType:   event.Type,
		sequence:    sequence,
	})
}

func (s *BkLogSidecar) enqueuePendingContainerCleanup(containerID string, generation uint64) {
	s.getOrCreateContainerEventQueue().AddRateLimited(containerWorkItem{
		kind:              containerWorkPendingCleanup,
		containerID:       containerID,
		pendingGeneration: generation,
	})
}

func (s *BkLogSidecar) processNextContainerWorkItem(
	ctx context.Context,
	queue workqueue.RateLimitingInterface,
) bool {
	rawItem, shutdown := queue.Get()
	if shutdown {
		return false
	}
	defer queue.Done(rawItem)

	item, ok := rawItem.(containerWorkItem)
	if !ok {
		queue.Forget(rawItem)
		s.log.Info(fmt.Sprintf("ignore unexpected container work item type %T", rawItem))
		return true
	}

	// 首次收到的事件仍按 Runtime 顺序执行；只有旧事件的“重试副本”才会在出现
	// 更新事件后被丢弃，避免 CREATE 重试晚于 STOP/DELETE 而重新写回过期配置。
	if item.kind == containerWorkEvent && queue.NumRequeues(item) > 0 && !s.isLatestContainerEvent(item) {
		queue.Forget(item)
		s.log.Info("drop stale retried container event",
			"containerID", item.containerID,
			"eventType", item.eventType,
			"sequence", item.sequence,
		)
		return true
	}

	err := s.processContainerWorkItem(ctx, item)
	if err == nil {
		queue.Forget(item)
		if item.kind == containerWorkEvent {
			s.clearLatestContainerEvent(item)
		}
		return true
	}

	if item.kind == containerWorkEvent && !s.isLatestContainerEvent(item) {
		queue.Forget(item)
		s.log.Error(err, "drop failed container event superseded by newer event",
			"containerID", item.containerID,
			"eventType", item.eventType,
			"sequence", item.sequence,
		)
		return true
	}

	queue.AddRateLimited(item)
	s.log.Error(err, "container work item failed, retrying with rate limit",
		"kind", item.kind,
		"containerID", item.containerID,
		"eventType", item.eventType,
		"retryCount", queue.NumRequeues(item),
	)
	return true
}

func (s *BkLogSidecar) processContainerWorkItem(ctx context.Context, item containerWorkItem) error {
	switch item.kind {
	case containerWorkEvent:
		return s.eventHandler(ctx, &define.ContainerEvent{
			ContainerID: item.containerID,
			Type:        item.eventType,
		})
	case containerWorkPendingCleanup:
		return s.finishPendingContainerDeletion(item.containerID, item.pendingGeneration)
	default:
		return nil
	}
}

func (s *BkLogSidecar) isLatestContainerEvent(item containerWorkItem) bool {
	s.eventSequenceMu.Lock()
	defer s.eventSequenceMu.Unlock()
	return s.latestEventSequence[item.containerID] == item.sequence
}

func (s *BkLogSidecar) clearLatestContainerEvent(item containerWorkItem) {
	s.eventSequenceMu.Lock()
	defer s.eventSequenceMu.Unlock()
	if s.latestEventSequence[item.containerID] == item.sequence {
		delete(s.latestEventSequence, item.containerID)
	}
}
