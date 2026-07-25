// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package controllers

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

type bkLogConfigKey struct {
	namespace string
	name      string
}

type bkLogConfigReconcileState struct {
	key     bkLogConfigKey
	current *bluekingv1alpha1.BkLogConfig
}

type configGenerationOptions struct {
	forceReload            bool
	refreshDiscoveredState bool
	reconcile              *bkLogConfigReconcileState
}

type pendingContainerDeletion struct {
	generation           uint64
	deadline             time.Time
	deleteContainerCache bool
}

// preservePendingContainerConfigsLocked 将仍在 DelayCleanConfig 宽限期内的
// 容器配置合并回全量 desired。来源 CR 已更新或删除的旧配置不会合并，仍按
// 最新声明立即收敛；没有变化的 CR resync 则继续尊重容器宽限期。
func (s *BkLogSidecar) preservePendingContainerConfigsLocked(
	desired desiredConfigSet,
	reconcile *bkLogConfigReconcileState,
) error {
	now := time.Now()
	desiredContainerIDs := containerIDsInDesired(desired)

	// 如果同一个容器 ID 已重新出现在完整 desired 中，说明它已经重新运行；
	// 取消旧的延迟删除，避免旧定时器误删新配置。
	for containerID := range s.pendingContainerDeletes {
		if _, ok := desiredContainerIDs[containerID]; ok {
			delete(s.pendingContainerDeletes, containerID)
		}
	}

	type pendingDesiredConfig struct {
		name      string
		generated desiredConfig
	}
	candidates := make(map[string][]pendingDesiredConfig)
	var renderErr error
	s.actualBkLogConfigCache.Range(func(key, value interface{}) bool {
		name := key.(string)
		if _, ok := desired[name]; ok {
			return true
		}
		logConfig := value.(define.LogConfigType)
		containerID, ok := containerIDFromLogConfig(logConfig)
		if !ok {
			// Node 配置不属于容器退出宽限期，应由 full Apply 正常裁剪。
			return true
		}
		if _, ok := desiredContainerIDs[containerID]; ok {
			// 容器仍在本轮 desired 中时，只能说明某一条配置已经失配。
			// 此类配置应立即裁剪，不能提升为整个容器的退出宽限期，
			// 否则到期清理会把同容器仍然有效的其他配置一并删除。
			return true
		}
		if reconcile != nil && logConfigBelongsTo(logConfig, reconcile.key) &&
			!logConfigSourceUnchanged(logConfig, reconcile.current) {
			return true
		}
		content, err := logConfig.Config()
		if err != nil {
			renderErr = fmt.Errorf("render pending config %s: %w", name, err)
			return false
		}
		candidates[containerID] = append(candidates[containerID], pendingDesiredConfig{
			name: name,
			generated: desiredConfig{
				logConfig: logConfig,
				content:   content,
			},
		})
		return true
	})
	if renderErr != nil {
		return renderErr
	}

	for containerID, configs := range candidates {
		// 完整 desired 已确认整个容器消失。即使 runtime 删除事件漏报，
		// 全量收敛也会建立相同宽限期，并在到期后清理 containerCache。
		// 若 STOP 事件已先建立宽限期，ensure 会升级缓存清理意图但不会重置 deadline。
		pending, scheduled := s.ensurePendingContainerDeletionLocked(containerID, true)
		if scheduled {
			s.schedulePendingContainerCleanup(containerID, pending.generation, time.Until(pending.deadline))
		}
		if !now.Before(pending.deadline) {
			continue
		}
		for _, candidate := range configs {
			desired[candidate.name] = candidate.generated
		}
	}
	return nil
}

func containerIDsInDesired(desired desiredConfigSet) map[string]struct{} {
	containerIDs := make(map[string]struct{})
	for _, generated := range desired {
		if containerID, ok := containerIDFromLogConfig(generated.logConfig); ok {
			containerIDs[containerID] = struct{}{}
		}
	}
	return containerIDs
}

func containerIDFromLogConfig(logConfig define.LogConfigType) (string, bool) {
	switch typed := logConfig.(type) {
	case *define.StdOutLogConfig:
		if typed.Container != nil && typed.Container.ID != "" {
			return typed.Container.ID, true
		}
	case *define.ContainerLogConfig:
		if typed.Container != nil && typed.Container.ID != "" {
			return typed.Container.ID, true
		}
	case *define.NodeLogConfig:
		return "", false
	}

	// 测试替身及历史实现都遵循“containerID_配置类型_...”命名规则。
	containerID, _, ok := strings.Cut(logConfig.ConfigName(), "_")
	return containerID, ok && containerID != ""
}

func logConfigBelongsTo(logConfig define.LogConfigType, key bkLogConfigKey) bool {
	switch typed := logConfig.(type) {
	case *define.StdOutLogConfig:
		return typed.Namespace == key.namespace && typed.Name == key.name
	case *define.ContainerLogConfig:
		return typed.Namespace == key.namespace && typed.Name == key.name
	case *define.NodeLogConfig:
		return typed.Namespace == key.namespace && typed.Name == key.name
	}
	return strings.HasSuffix(logConfig.ConfigName(), "_"+key.namespace+"_"+key.name)
}

func logConfigSourceUnchanged(logConfig define.LogConfigType, current *bluekingv1alpha1.BkLogConfig) bool {
	if current == nil {
		return false
	}
	var cached *bluekingv1alpha1.BkLogConfig
	switch typed := logConfig.(type) {
	case *define.StdOutLogConfig:
		cached = &typed.BkLogConfig
	case *define.ContainerLogConfig:
		cached = &typed.BkLogConfig
	case *define.NodeLogConfig:
		cached = &typed.BkLogConfig
	default:
		// 未知实现无法证明来源 CR 没有变化，按安全侧让本次 reconcile 裁剪。
		return false
	}
	if cached.UID != "" && current.UID != "" && cached.UID != current.UID {
		return false
	}
	cachedSpec := cached.Spec
	currentSpec := current.Spec
	// TailFiles 是 sidecar 按容器新旧动态计算的运行时字段，不属于 CR 声明差异。
	cachedSpec.TailFiles = false
	currentSpec.TailFiles = false
	return reflect.DeepEqual(cachedSpec, currentSpec) &&
		cached.Labels[config.BkEnvLabelName] == current.Labels[config.BkEnvLabelName]
}

func (s *BkLogSidecar) scheduleContainerConfigDeletion(container *define.Container, deleteContainerCache bool) {
	s.configMutationMu.Lock()
	pending, scheduled := s.ensurePendingContainerDeletionLocked(container.ID, deleteContainerCache)
	s.configMutationMu.Unlock()
	if scheduled {
		s.schedulePendingContainerCleanup(container.ID, pending.generation, time.Until(pending.deadline))
	}
}

func (s *BkLogSidecar) ensurePendingContainerDeletionLocked(
	containerID string,
	deleteContainerCache bool,
) (*pendingContainerDeletion, bool) {
	if s.pendingContainerDeletes == nil {
		s.pendingContainerDeletes = make(map[string]*pendingContainerDeletion)
	}
	if pending, ok := s.pendingContainerDeletes[containerID]; ok {
		pending.deleteContainerCache = pending.deleteContainerCache || deleteContainerCache
		return pending, false
	}

	s.pendingDeleteGeneration++
	pending := &pendingContainerDeletion{
		generation:           s.pendingDeleteGeneration,
		deadline:             time.Now().Add(time.Duration(config.DelayCleanConfig) * time.Second),
		deleteContainerCache: deleteContainerCache,
	}
	s.pendingContainerDeletes[containerID] = pending
	return pending, true
}

func (s *BkLogSidecar) schedulePendingContainerCleanup(containerID string, generation uint64, delay time.Duration) {
	cleanup := func() {
		if err := s.finishPendingContainerDeletion(containerID, generation); err != nil {
			s.log.Error(err, "delete configs for container event failed", "containerID", containerID)
			// 测试接缝直接执行回调时，真实失败仍交给 workqueue 限速重试。
			s.enqueuePendingContainerCleanup(containerID, generation)
		}
	}
	if s.delayCleanFn != nil {
		s.delayCleanFn(delay, cleanup)
		return
	}

	// 生产路径复用已有 delaying workqueue 管理 DelayCleanConfig，而不是创建
	// 无法随 controller-runtime Context 退出的独立 timer goroutine。
	s.getOrCreateContainerEventQueue().AddAfter(containerWorkItem{
		kind:              containerWorkPendingCleanup,
		containerID:       containerID,
		pendingGeneration: generation,
	}, delay)
}

func (s *BkLogSidecar) finishPendingContainerDeletion(containerID string, generation uint64) error {
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()

	pending, ok := s.pendingContainerDeletes[containerID]
	if !ok {
		// 文件事务成功但 reload 失败时 pending 已移除，重试项只需基于当前
		// cache 补发 reload，不能把已经删除的旧容器配置重新合并回来。
		if !s.reloadPending {
			return nil
		}
		desired, err := s.desiredConfigsFromCacheLocked()
		if err != nil {
			return fmt.Errorf("render current config snapshot for pending reload: %w", err)
		}
		return s.applyDesiredConfigsLocked(desired, false, nil, convergenceTriggerContainerCleanup)
	}
	if pending.generation != generation {
		return nil
	}
	if pending.deleteContainerCache {
		s.containerCache.Delete(containerID)
	}
	err := s.deleteContainerConfigLocked(&define.Container{ID: containerID})
	if !s.actualConfigCacheContainsContainerLocked(containerID) {
		// 文件事务成功但 reload 失败时，配置 cache 已经完成删除；此时只保留
		// reloadPending 即可，不能让过期宽限记录再次把旧配置合并回来。
		delete(s.pendingContainerDeletes, containerID)
	}
	return err
}

func (s *BkLogSidecar) actualConfigCacheContainsContainerLocked(containerID string) bool {
	found := false
	s.actualBkLogConfigCache.Range(func(_, value interface{}) bool {
		id, ok := containerIDFromLogConfig(value.(define.LogConfigType))
		if ok && id == containerID {
			found = true
			return false
		}
		return true
	})
	return found
}

func (s *BkLogSidecar) cancelPendingContainerDeletion(containerID string) {
	s.configMutationMu.Lock()
	delete(s.pendingContainerDeletes, containerID)
	s.configMutationMu.Unlock()
}
