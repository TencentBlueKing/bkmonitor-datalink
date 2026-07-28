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
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

const (
	SubscribeRetryInterval       = 5 * time.Second
	SubscriptionStabilityWindow  = time.Second
	CreateEventVisibilityWindow  = 10 * time.Second
	RuntimeOperationTimeout      = 10 * time.Second
	ConvergenceRetryBaseDelay    = time.Second
	ConvergenceRetryMaximumDelay = 30 * time.Second
	// 一次快照冲突通常只是事件与全量 Build 短暂重叠，允许就地合并一次；
	// 持续冲突则交给外层队列或周期退避，避免在高 churn 节点上忙等。
	maxImmediateConfigSnapshotRetries = 1
)

var errConfigSnapshotChanged = errors.New("configuration snapshot changed during build")

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

// BkLogSidecar BkLogSidecar
type BkLogSidecar struct {
	runtime                     define.Runtime
	runtimeMu                   sync.Mutex
	kubeClient                  client.Reader
	reloadAgentFn               func() error
	delayCleanFn                func(time.Duration, func())
	eventQueueMu                sync.Mutex
	eventQueue                  workqueue.RateLimitingInterface
	eventWorkerOnce             sync.Once
	eventShutdownOnce           sync.Once
	lifecycleWG                 sync.WaitGroup
	eventSequenceMu             sync.Mutex
	eventSequence               uint64
	latestEventSequence         map[string]uint64
	subscribeRetryInterval      time.Duration
	subscriptionStabilityWindow time.Duration
	createEventVisibilityWindow time.Duration
	runtimeOperationTimeout     time.Duration
	convergenceRetryBaseDelay   time.Duration
	convergenceRetryMaxDelay    time.Duration
	periodicReconcileInterval   time.Duration
	periodicReconcileJitter     float64
	periodicReconcileDelayFn    func(time.Duration, float64) time.Duration
	// configMutationMu 保护配置快照、延迟删除状态、磁盘事务与 reload 状态，不包围
	// Runtime/Kubernetes 查询；外部查询通过 configGeneration 做乐观校验。
	configMutationMu sync.Mutex
	// configGeneration 不仅表示已 Apply 的配置版本，也覆盖会改变下一轮 desired 的
	// pending 删除与 CREATE 状态，防止旧 Build 覆盖并发到达的容器事件。
	configGeneration uint64
	reloadPending    bool
	// pendingContainerDeletes 只在 configMutationMu 保护下访问，用于保证容器退出后的
	// DelayCleanConfig 宽限期不会被并发的全量配置收敛提前裁剪。
	pendingContainerDeletes map[string]*pendingContainerDeletion
	pendingDeleteGeneration uint64
	// pendingContainerCreates 记录 Runtime CREATE 到 Inspect 最终可见之间的短暂窗口。
	// 周期全量会据此沿用“新容器从头采集”语义；窗口结束后不再无限放大流量风险。
	pendingContainerCreates map[string]pendingContainerCreation
	containerCache          sync.Map
	nodeInfoMu              sync.RWMutex
	currentNodeInfo         corev1.Node
	actualBkLogConfigCache  sync.Map
	log                     logr.Logger
	stopCh                  chan struct{}
	stopOnce                sync.Once
}

func (s *BkLogSidecar) reloadAgent() error {
	if s.reloadAgentFn != nil {
		return s.reloadAgentFn()
	}
	return s.reloadBkunifylogbeat()
}

// NewBkLogSidecar new BkLogSidecar
func NewBkLogSidecar(mgr ctrl.Manager) *BkLogSidecar {
	bkLogSidecar := &BkLogSidecar{
		stopCh:                    make(chan struct{}),
		log:                       ctrl.Log.WithName("bkLogSidecar"),
		kubeClient:                mgr.GetCache(),
		pendingContainerDeletes:   make(map[string]*pendingContainerDeletion),
		pendingContainerCreates:   make(map[string]pendingContainerCreation),
		periodicReconcileInterval: config.PeriodicReconcileInterval,
		periodicReconcileJitter:   config.PeriodicReconcileJitter,
	}
	return bkLogSidecar
}

// Start start bklog sidecar
func (s *BkLogSidecar) Start(ctx context.Context) error {
	s.log.Info("start bklog sidecar")
	runCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		// Start 必须持有所有后台任务的完整生命周期。先停止接收新任务并
		// 等待队列中的配置事务完成，再交还 controller-runtime。
		s.shutdownContainerEventQueue()
		s.lifecycleWG.Wait()
	}()

	s.startContainerEventWorker(runCtx)
	subscriptionReady := make(chan struct{})
	s.lifecycleWG.Add(1)
	go func() {
		defer s.lifecycleWG.Done()
		s.subscribeEvent(runCtx, subscriptionReady)
	}()
	select {
	case <-subscriptionReady:
		// 首次配置已经成功收敛；订阅持续不可用时也可能由降级路径放行。
	case <-runCtx.Done():
		return nil
	case <-s.stopCh:
		cancel()
		return nil
	}

	s.lifecycleWG.Add(1)
	go func() {
		defer s.lifecycleWG.Done()
		s.periodicReconcile(runCtx)
	}()

	// controller-runtime Runnable 要求 Start 阻塞到 Context 取消或出错。
	select {
	case <-runCtx.Done():
	case <-s.stopCh:
		cancel()
	}
	return nil
}

// Stop stop bklog sidecar
func (s *BkLogSidecar) Stop() {
	s.stopOnce.Do(func() {
		s.log.Info("stop bklog sidecar")
		close(s.stopCh)
	})
}

func (s *BkLogSidecar) getRuntime() (define.Runtime, error) {
	return s.getRuntimeWithContext(context.Background())
}

func (s *BkLogSidecar) getRuntimeWithContext(ctx context.Context) (define.Runtime, error) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtime != nil {
		return s.runtime, nil
	}

	// Node 缓存暂时不可用时直接返回，让调用方重试；不能再拿空版本创建 Runtime，
	// 更不能由底层构造函数退出整个 sidecar 进程。
	if err := s.refreshNodeInfoWithContext(ctx); err != nil {
		return nil, fmt.Errorf("refresh node info before runtime initialization: %w", err)
	}
	node := s.currentNodeSnapshot()
	runtime, err := NewRuntime(node.Status.NodeInfo.ContainerRuntimeVersion)
	if err != nil {
		return nil, err
	}
	s.runtime = runtime
	return s.runtime, nil
}

// initNodeInfo
func (s *BkLogSidecar) refreshNodeInfo() error {
	return s.refreshNodeInfoWithContext(context.Background())
}

func (s *BkLogSidecar) refreshNodeInfoWithContext(ctx context.Context) error {
	nodeName := os.Getenv(config.CurrentNodeNameKey)
	if !utils.StringNotEmpty(nodeName) {
		return fmt.Errorf("environment variable %s is empty", config.CurrentNodeNameKey)
	}
	var node corev1.Node
	err := s.kubeClient.Get(ctx, client.ObjectKey{
		Name: nodeName,
	}, &node)
	if err != nil {
		return fmt.Errorf("get Node %s: %w", nodeName, err)
	}
	s.nodeInfoMu.Lock()
	s.currentNodeInfo = node
	s.nodeInfoMu.Unlock()
	s.log.V(2).Info(fmt.Sprintf("current node info is [%s], labels[%v]", node.Name, node.GetLabels()))
	return nil
}

func (s *BkLogSidecar) currentNodeSnapshot() corev1.Node {
	s.nodeInfoMu.RLock()
	defer s.nodeInfoMu.RUnlock()
	return *s.currentNodeInfo.DeepCopy()
}

// generateActualBkLogConfig will generate all actual bklog config
func (s *BkLogSidecar) generateActualBkLogConfig() error {
	return s.generateActualBkLogConfigWithOptions(context.Background(), configGenerationOptions{})
}

func (s *BkLogSidecar) generateActualBkLogConfigOnStartup(ctx context.Context) error {
	return s.generateActualBkLogConfigWithOptions(ctx, configGenerationOptions{forceReload: true})
}

func (s *BkLogSidecar) generateActualBkLogConfigForPeriodicReconcile(ctx context.Context) error {
	return s.generateActualBkLogConfigWithOptions(ctx, configGenerationOptions{refreshDiscoveredState: true})
}

func (s *BkLogSidecar) generateActualBkLogConfigForReconcile(
	ctx context.Context,
	namespace, name string,
	current *v1alpha1.BkLogConfig,
) error {
	return s.generateActualBkLogConfigWithOptions(ctx, configGenerationOptions{
		reconcile: &bkLogConfigReconcileState{
			key:     bkLogConfigKey{namespace: namespace, name: name},
			current: current,
		},
	})
}

func (s *BkLogSidecar) generateActualBkLogConfigWithOptions(
	ctx context.Context,
	options configGenerationOptions,
) error {
	if options.forceReload {
		s.configMutationMu.Lock()
		// sidecar 重启会丢失内存中的 reloadPending。启动时先恢复该意图，
		// 即使首次 Build 失败，后续任一成功收敛也仍会补发 reload。
		s.reloadPending = true
		s.configMutationMu.Unlock()
	}

	snapshotRetries := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Build 阶段包含 Runtime/Kubernetes I/O，不能持有配置写锁。记录世代后
		// 在 Apply 前复核；若期间有事件提交了新快照，就基于最新状态重新 Build。
		generation := s.configSnapshotGeneration()
		buildResult, err := s.buildActualBkLogConfigs(ctx, options)
		if err != nil {
			return err
		}
		desired, err := renderDesiredConfigs(buildResult.logConfigs)
		if err != nil {
			return fmt.Errorf("render desired log configs: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		s.configMutationMu.Lock()
		currentGeneration := s.configGeneration
		if generation != currentGeneration {
			s.configMutationMu.Unlock()
			if err := configSnapshotRetryError(
				"full configuration reconciliation",
				snapshotRetries,
				generation,
				currentGeneration,
			); err != nil {
				return err
			}
			snapshotRetries++
			continue
		}
		// Runtime 的全量列表只包含运行中容器。这里显式合并仍处于退出宽限期的配置，
		// 避免其他 BkLogConfig 的 reconcile 提前结束尾部日志采集。
		if err := s.preservePendingContainerConfigsLocked(desired, options.reconcile); err != nil {
			s.configMutationMu.Unlock()
			return fmt.Errorf("preserve pending container configs: %w", err)
		}
		err = s.applyDesiredConfigsLocked(desired, true, nil, options.trigger())
		if err == nil {
			// containerCache 的收敛依据来自独立的 Runtime List 快照，不能只从
			// 实际生成过配置的容器反推，否则 STOP 清理完成后会永久失去删除线索。
			s.reconcileContainerCacheLocked(buildResult.discoveredContainerIDs)
		}
		s.configMutationMu.Unlock()
		return err
	}
}

// configSnapshotRetryError 限制单次调用内的乐观重试次数。达到边界后返回
// 可识别错误，让调用方沿用现有的 workqueue/controller-runtime/周期退避。
func configSnapshotRetryError(
	operation string,
	retryCount int,
	snapshotGeneration, currentGeneration uint64,
) error {
	if retryCount < maxImmediateConfigSnapshotRetries {
		return nil
	}
	return fmt.Errorf(
		"%w: %s exceeded %d immediate retries (snapshot generation %d, current generation %d)",
		errConfigSnapshotChanged,
		operation,
		maxImmediateConfigSnapshotRetries,
		snapshotGeneration,
		currentGeneration,
	)
}

type actualConfigBuildResult struct {
	logConfigs             []define.LogConfigType
	discoveredContainerIDs map[string]struct{}
}

// buildActualBkLogConfigs discovers the complete desired snapshot without
// mutating the active config cache or any on-disk file. Runtime/Node metadata
// caches may be refreshed during discovery.
func (s *BkLogSidecar) buildActualBkLogConfigs(
	ctx context.Context,
	options configGenerationOptions,
) (actualConfigBuildResult, error) {
	result := actualConfigBuildResult{}
	var err error
	if options.refreshDiscoveredState {
		// 周期校准必须重新读取 Node；即使当前没有 node_log_config，也要避免
		// 后续匹配继续使用长期不更新的标签和 annotation。
		if err := s.refreshNodeInfoWithContext(ctx); err != nil {
			return actualConfigBuildResult{}, fmt.Errorf(
				"refresh node info for periodic reconciliation: %w",
				err,
			)
		}
	}
	// 一次 Build 只读取一份 BkLogConfig 快照，并同时用于所有容器和 Node。
	// 这样既避免按容器重复 DeepCopy 全量 informer cache，也不会在同一 desired
	// 中混入一次 CR 更新前后的两个版本。
	allBklogConfigs, err := s.bkLogConfigList(ctx)
	if err != nil {
		return actualConfigBuildResult{}, fmt.Errorf(
			"list BkLogConfigs for full configuration build: %w",
			err,
		)
	}
	result.logConfigs, result.discoveredContainerIDs, err = s.allContainerBkLogConfigs(
		ctx,
		result.logConfigs,
		options.refreshDiscoveredState,
		allBklogConfigs,
	)
	if err != nil {
		// An incomplete discovery result must never be treated as the desired
		// state, otherwise valid files could be deleted from a partial snapshot.
		return actualConfigBuildResult{}, fmt.Errorf("build container log configs: %w", err)
	}
	// match all node_log_config
	firstMatchNodeConfig := !options.refreshDiscoveredState
	for _, bkLogConfig := range allBklogConfigs {
		if !bkLogConfig.IsNodeType() {
			continue
		}
		if firstMatchNodeConfig {
			if err := s.refreshNodeInfoWithContext(ctx); err != nil {
				return actualConfigBuildResult{}, fmt.Errorf(
					"refresh node info for node log config matching: %w",
					err,
				)
			}
			firstMatchNodeConfig = false
		}
		node := s.currentNodeSnapshot()
		// label match
		if !s.matchLabel(bkLogConfig.Spec.LabelSelector, node.GetLabels()) {
			s.log.V(2).Info("current node not match label")
			continue
		}
		// annotation match
		if !s.matchAnnotation(bkLogConfig.Spec.AnnotationSelector, node.GetAnnotations()) {
			s.log.V(2).Info("current node not match annotation")
			continue
		}
		s.log.V(2).Info(fmt.Sprintf("[%s] log config match node[%s]", bkLogConfig.Name, node.Name))
		result.logConfigs = append(result.logConfigs, &define.NodeLogConfig{
			BkLogConfig: bkLogConfig,
			Node:        &node,
		})
	}

	if define.Empty(result.logConfigs) {
		s.log.V(2).Info("not have log config")
	}
	return result, nil
}

// allContainerBkLogConfigs will match all container log config (std and container log)
func (s *BkLogSidecar) allContainerBkLogConfigs(
	ctx context.Context,
	logConfigs []define.LogConfigType,
	refreshContainerInfo bool,
	bkLogConfigs []v1alpha1.BkLogConfig,
) ([]define.LogConfigType, map[string]struct{}, error) {
	allContainer, err := s.allContainersWithContext(ctx)
	if err != nil {
		return logConfigs, nil, fmt.Errorf("list runtime containers: %w", err)
	}
	discoveredContainerIDs := make(map[string]struct{}, len(allContainer))
	for i, container := range allContainer {
		if container.ID != "" {
			discoveredContainerIDs[container.ID] = struct{}{}
		}
		// 周期全量校准会遍历每个容器。容器与规则的逐项匹配明细只用于深度
		// 排查，统一放到默认关闭的 V(2)，避免按周期产生 O(容器数×配置数) 日志。
		s.log.V(2).Info(fmt.Sprintf("container info -> [%d] [%s]", i, container.ID))
		c, ok := s.containerCache.Load(container.ID)
		isNewContainer := s.isPendingContainerCreate(container.ID)
		if ok && !refreshContainerInfo {
			containerInfo := castContainer(c)
			logConfigs, err = s.containerBkLogConfigs(
				ctx, containerInfo, logConfigs, isNewContainer, bkLogConfigs,
			)
			if err != nil {
				return logConfigs, discoveredContainerIDs, err
			}
			continue
		}
		// 周期校准不复用旧 containerCache，强制从 Runtime Inspect 当前状态；
		// 仍只做一次 Runtime List，避免“先刷新缓存、再全量 Build”的双重扫描。
		containerInfo, err := s.containerByIDWithContext(ctx, container.ID)
		if err != nil {
			return logConfigs, discoveredContainerIDs, err
		}
		if containerInfo == nil {
			continue
		}
		s.containerCache.Store(container.ID, containerInfo)
		logConfigs, err = s.containerBkLogConfigs(
			ctx, containerInfo, logConfigs, isNewContainer, bkLogConfigs,
		)
		if err != nil {
			return logConfigs, discoveredContainerIDs, err
		}
	}
	return logConfigs, discoveredContainerIDs, nil
}

// containerBkLogConfigs will return single container all relation log config
func (s *BkLogSidecar) containerBkLogConfigs(
	ctx context.Context,
	container *define.Container,
	logConfigs []define.LogConfigType,
	isNewContainer bool,
	bkLogConfigs []v1alpha1.BkLogConfig,
) ([]define.LogConfigType, error) {
	matchBklogConfigs, pod, err := s.matchBklogConfigs(ctx, container, bkLogConfigs)
	if err != nil {
		return logConfigs, fmt.Errorf("match log configs for container %s: %w", container.ID, err)
	}
	for _, bkLogConfig := range matchBklogConfigs {
		// 对于新增容器的场景，需要从头开始采集日志文件
		bkLogConfig.Spec.TailFiles = !isNewContainer // stdout and stderr collect log from beginning

		var logConfig define.LogConfigType
		if bkLogConfig.IsContainerType() {
			logConfig = &define.ContainerLogConfig{
				BkLogConfig: bkLogConfig,
				Container:   container,
				Pod:         pod,
			}
		} else {
			runtime, err := s.getRuntimeWithContext(ctx)
			if err != nil {
				return logConfigs, fmt.Errorf("get runtime type for container %s: %w", container.ID, err)
			}
			logConfig = &define.StdOutLogConfig{
				BkLogConfig: bkLogConfig,
				Container:   container,
				Pod:         pod,
				RuntimeType: runtime.Type(),
			}
		}
		if !isNewContainer {
			// actual cache 与 configGeneration 共同构成乐观快照；若并发
			// Apply 替换了 cache，外层的世代复核会重新 Build。
			s.preserveRuntimeTailFiles(logConfig)
		}
		logConfigs = append(logConfigs, logConfig)
	}
	return logConfigs, nil
}

// allContainers will all container info
func (s *BkLogSidecar) allContainers() ([]define.SimpleContainer, error) {
	return s.allContainersWithContext(context.Background())
}

func (s *BkLogSidecar) allContainersWithContext(parent context.Context) ([]define.SimpleContainer, error) {
	ctx, cancel := context.WithTimeout(parent, s.getRuntimeOperationTimeout())
	defer cancel()
	runtime, err := s.getRuntimeWithContext(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Containers(ctx)
}

// eventHandler handler event
func (s *BkLogSidecar) eventHandler(ctx context.Context, event *define.ContainerEvent) error {
	if event == nil {
		return nil
	}
	switch event.Type {
	case define.ContainerEventCreate:
		return s.startActionHandler(ctx, event)
	case define.ContainerEventDelete:
		return s.destroyActionHandler(event)
	case define.ContainerEventStop:
		return s.stopActionHandler(ctx, event)
	default:
		s.log.Info(fmt.Sprintf("not expecting event type [%s] for container [%s]", event.Type, event.ContainerID))
		return nil
	}
}

// startActionHandler handler start event
func (s *BkLogSidecar) startActionHandler(ctx context.Context, event *define.ContainerEvent) error {
	s.log.Info(fmt.Sprintf("start handler [%s] for container [%s]", event.Type, event.ContainerID))

	// CREATE 必须向 Runtime 重新确认，不能直接相信 stop/delete 前留下的 cache；
	// 否则乱序或重放事件可能取消真实的待删除任务并重新写回旧配置。
	container, err := s.inspectContainerWithContext(ctx, event.ContainerID)
	if err != nil {
		return fmt.Errorf("get container for create event %s: %w", event.ContainerID, err)
	}
	if container == nil {
		s.log.Info(fmt.Sprintf("container [%s] not exists, do nothing for action [%s].", event.ContainerID, event.Type))
		return nil
	}
	// 同一个容器 ID 可能在 stop 后再次收到 start（例如 runtime 事件重放）。
	// cache 写入和取消旧删除必须作为一个带世代推进的状态变更提交，否则并发
	// 全量 Build 可能拿旧 Runtime 快照反向清掉刚启动的容器。
	s.storeStartedContainer(container)

	matched, err := s.upsertContainerConfigsWithContext(ctx, container, true)
	if err != nil {
		return fmt.Errorf("build or apply configs for create event %s: %w", event.ContainerID, err)
	}
	if !matched {
		s.log.Info(fmt.Sprintf("container [%s] not match log config", container.ID))
		return nil
	}
	s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
	return nil
}

func (s *BkLogSidecar) upsertContainerConfigs(container *define.Container, isNewContainer bool) (bool, error) {
	return s.upsertContainerConfigsWithContext(context.Background(), container, isNewContainer)
}

func (s *BkLogSidecar) upsertContainerConfigsWithContext(
	ctx context.Context,
	container *define.Container,
	isNewContainer bool,
) (bool, error) {
	snapshotRetries := 0
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		generation := s.configSnapshotGeneration()
		// CREATE 是单容器增量更新，但一次重试也必须只使用一份 CR 快照。
		bkLogConfigs, err := s.bkLogConfigList(ctx)
		if err != nil {
			return false, fmt.Errorf("list BkLogConfigs for container %s: %w", container.ID, err)
		}
		logConfigs, err := s.containerBkLogConfigs(ctx, container, nil, isNewContainer, bkLogConfigs)
		if err != nil {
			return false, err
		}
		if define.Empty(logConfigs) {
			// 空匹配也必须校验世代，否则可能恰好错过并发新增的 BkLogConfig。
			currentGeneration := s.configSnapshotGeneration()
			if generation == currentGeneration {
				return false, nil
			}
			if err := configSnapshotRetryError(
				"container CREATE configuration upsert",
				snapshotRetries,
				generation,
				currentGeneration,
			); err != nil {
				return false, err
			}
			snapshotRetries++
			continue
		}
		applied, err := s.upsertActualConfigsIfCurrent(logConfigs, generation)
		if err != nil {
			return false, err
		}
		if applied {
			return true, nil
		}
		// 并发全量收敛已经提交了更新，旧事件不能覆盖它；重新读取最新资源后再合并。
		currentGeneration := s.configSnapshotGeneration()
		if err := configSnapshotRetryError(
			"container CREATE configuration upsert",
			snapshotRetries,
			generation,
			currentGeneration,
		); err != nil {
			return false, err
		}
		snapshotRetries++
	}
}

// destroyActionHandler handler destroy event
func (s *BkLogSidecar) destroyActionHandler(event *define.ContainerEvent) error {
	s.log.Info(fmt.Sprintf("start handler [%s] for container [%s]", event.Type, event.ContainerID))
	containerInfo, ok := s.containerCache.Load(event.ContainerID)
	if ok {
		s.scheduleContainerConfigDeletion(castContainer(containerInfo), true)
	}
	s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
	return nil
}

// stopActionHandler handler stop event
func (s *BkLogSidecar) stopActionHandler(ctx context.Context, event *define.ContainerEvent) error {
	s.log.Info(fmt.Sprintf("start handler [%s] for container [%s]", event.Type, event.ContainerID))

	container, err := s.getContainerInfoByIDWithContext(ctx, event.ContainerID)
	if err != nil {
		return fmt.Errorf("get container for stop event %s: %w", event.ContainerID, err)
	}
	if container == nil {
		s.log.Info(fmt.Sprintf("container [%s] not exists, do nothing for action [%s].", event.ContainerID, event.Type))
		return nil
	}

	s.scheduleContainerConfigDeletion(container, false)
	s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
	return nil
}

// bkLogConfigList will get all BkLogConfig from k8s
func (s *BkLogSidecar) bkLogConfigList(ctx context.Context) ([]v1alpha1.BkLogConfig, error) {
	var bkLogConfigs v1alpha1.BkLogConfigList
	err := s.kubeClient.List(ctx, &bkLogConfigs)
	if err != nil {
		return nil, err
	}

	var filteredConfigs []v1alpha1.BkLogConfig
	for _, bkLogConfig := range bkLogConfigs.Items {
		// 过滤 bk-env
		if bkLogConfig.IsMatchBkEnv(config.BkEnvs) {
			filteredConfigs = append(filteredConfigs, bkLogConfig)
		} else {
			s.log.V(2).Info(fmt.Sprintf("resource [%s] with label `%s=\"%s\"` not in allowed values %v, ignored",
				bkLogConfig.Name, config.BkEnvLabelName,
				bkLogConfig.Labels[config.BkEnvLabelName], config.BkEnvs))
		}
	}
	// 默认日志只记录过滤结果汇总；逐资源明细保留在 V(2)，避免周期全量对账时大量刷屏。
	s.log.Info(
		"BkLogConfig environment filtering completed",
		"allowedBkEnvs", config.BkEnvs,
		"total", len(bkLogConfigs.Items),
		"matched", len(filteredConfigs),
		"ignored", len(bkLogConfigs.Items)-len(filteredConfigs),
	)
	return filteredConfigs, err
}

// matchBklogConfigs get target config
func (s *BkLogSidecar) matchBklogConfigs(
	ctx context.Context,
	container *define.Container,
	bkLogConfigs []v1alpha1.BkLogConfig,
) ([]v1alpha1.BkLogConfig, *corev1.Pod, error) {
	matchBkLogConfigs := make([]v1alpha1.BkLogConfig, 0)
	var pod corev1.Pod
	err := s.kubeClient.Get(ctx, client.ObjectKey{
		Namespace: container.Labels[config.ContainerLabelK8sPodNamespace],
		Name:      container.Labels[config.ContainerLabelK8sPodName],
	}, &pod)

	if apierrors.IsNotFound(err) {
		// A Pod may be deleted while its runtime container is still visible.
		// Treat that confirmed disappearance as a normal no-match result.
		return matchBkLogConfigs, &pod, nil
	}
	if err != nil {
		return matchBkLogConfigs, &pod, fmt.Errorf("get Pod for container %s: %w", container.ID, err)
	}

	containerName, ok := container.Labels[config.ContainerLabelK8sContainerName]
	if !ok {
		s.log.V(2).Info("container is not k8s container")
		return matchBkLogConfigs, &pod, nil
	}

	s.log.V(2).Info(fmt.Sprintf("container name is [%s]", containerName))
	if utils.IsNetworkPod(containerName) {
		return matchBkLogConfigs, &pod, nil
	}

	for _, bkLogConfig := range bkLogConfigs {
		// only std and container log can match
		if !bkLogConfig.IsNeedMatchType() {
			continue
		}

		if !s.matchNamespace(&bkLogConfig, &pod) {
			s.log.V(2).Info(fmt.Sprintf("container name is [%s] not match namespace", containerName))
			continue
		}

		// if set all_container is true direct match
		if bkLogConfig.Spec.AllContainer {
			s.log.V(2).Info(fmt.Sprintf("[%s] log config match container [%s]", bkLogConfig.Name, containerName))
			matchBkLogConfigs = append(matchBkLogConfigs, bkLogConfig)
			continue
		}

		// label match
		if !s.matchLabel(bkLogConfig.Spec.LabelSelector, pod.GetLabels()) {
			s.log.V(2).Info(fmt.Sprintf("container name is [%s] not match label", containerName))
			continue
		}

		// annotation match
		if !s.matchAnnotation(bkLogConfig.Spec.AnnotationSelector, pod.GetAnnotations()) {
			s.log.V(2).Info(fmt.Sprintf("container name is [%s] not match annotation", containerName))
			continue
		}

		// match container by container_name
		if !s.matchContainerName(containerName, bkLogConfig.Spec.ContainerNameMatch, bkLogConfig.Spec.ContainerNameExclude) {
			s.log.V(2).Info(fmt.Sprintf("container name is [%s] not match container name", containerName))
			continue
		}

		// match pod by workload config
		if !s.matchWorkload(&bkLogConfig, &pod) {
			s.log.V(2).Info(fmt.Sprintf("container name is [%s] not match workload", containerName))
			continue
		}
		s.log.V(2).Info(fmt.Sprintf("[%s] log config match container [%s]", bkLogConfig.Name, containerName))
		matchBkLogConfigs = append(matchBkLogConfigs, bkLogConfig)
	}
	return matchBkLogConfigs, &pod, nil
}

func (s *BkLogSidecar) matchLabel(matchSelector metav1.LabelSelector, matchLabels map[string]string) bool {
	s.log.V(2).Info(fmt.Sprintf("selector: %v, labels %v", matchSelector, matchLabels))
	selector, err := metav1.LabelSelectorAsSelector(&matchSelector)
	if utils.NotNil(err) {
		s.log.Error(err, "selector to label selector failed")
		return false
	}
	labelSet := labels.Set(matchLabels)
	if !selector.Matches(labelSet) {
		return false
	}
	s.log.V(2).Info(fmt.Sprintf("label match success %v", matchSelector))
	return true
}

func (s *BkLogSidecar) matchAnnotation(matchSelector metav1.LabelSelector, matchAnnotations map[string]string) bool {
	s.log.V(2).Info(fmt.Sprintf("selector: %v, annotations %v", matchSelector, matchAnnotations))
	selector, err := metav1.LabelSelectorAsSelector(&matchSelector)
	if utils.NotNil(err) {
		s.log.Error(err, "selector to label selector failed")
		return false
	}
	annotationSet := labels.Set(matchAnnotations)
	if !selector.Matches(annotationSet) {
		return false
	}
	s.log.V(2).Info(fmt.Sprintf("annotation match success %v", matchSelector))
	return true
}

func (s *BkLogSidecar) matchNamespace(bkLogConfig *v1alpha1.BkLogConfig, pod *corev1.Pod) bool {
	if bkLogConfig.Spec.NamespaceSelector.Any {
		return true
	} else {
		if len(bkLogConfig.Spec.NamespaceSelector.ExcludeNames) != 0 {
			// 全部不匹配true，否则为false
			for _, namespace := range bkLogConfig.Spec.NamespaceSelector.ExcludeNames {
				if pod.Namespace == namespace {
					s.log.V(2).Info(fmt.Sprintf("pod namespace [%s] match exclude namespace [%s]", pod.Namespace, namespace))
					return false
				}
			}
			return true
		} else if len(bkLogConfig.Spec.NamespaceSelector.MatchNames) != 0 {
			// 优先使用NamespaceSelector配置，列表中任意一个满足即可
			// 有一个匹配上则为true，否则直接false
			for _, namespace := range bkLogConfig.Spec.NamespaceSelector.MatchNames {
				if pod.Namespace == namespace {
					s.log.V(2).Info(fmt.Sprintf("pod namespace [%s] match namespace [%s]", pod.Namespace, namespace))
					return true
				}
			}
			return false
		} else {
			// 其次，使用Namespace配置，直接名字匹配
			if utils.StringNotEmpty(bkLogConfig.Spec.Namespace) {
				if pod.Namespace != bkLogConfig.Spec.Namespace {
					return false
				}
				s.log.V(2).Info(fmt.Sprintf("pod namespace [%s] match namespace [%s]", pod.Namespace, bkLogConfig.Spec.Namespace))
				return true
			}
			// 未配置则返回true
			return true
		}
	}
}

func (s *BkLogSidecar) matchWorkload(bkLogConfig *v1alpha1.BkLogConfig, pod *corev1.Pod) bool {
	if utils.StringNotEmpty(bkLogConfig.Spec.WorkloadType) {
		if !s.matchWorkloadType(bkLogConfig, pod) {
			return false
		}
	}

	if utils.StringNotEmpty(bkLogConfig.Spec.WorkloadName) {
		if !s.matchWorkloadName(bkLogConfig, pod) {
			return false
		}
	}
	return true
}

func (s *BkLogSidecar) matchWorkloadName(bkLogConfig *v1alpha1.BkLogConfig, pod *corev1.Pod) bool {
	r, err := regexp.Compile(bkLogConfig.Spec.WorkloadName)
	if utils.NotNil(err) {
		s.log.Error(err, "regexp compile failed")
		return false
	}

	var names []string

	if utils.IsVclusterPod(pod) {
		name := utils.GetPodWorkloadName(pod, "")
		kind := utils.GetPodWorkloadType(pod, "")
		names = append(names, utils.GetWorkloadName(name, kind))
	} else {
		for _, ownerReference := range pod.GetOwnerReferences() {
			names = append(names, utils.GetWorkloadName(ownerReference.Name, ownerReference.Kind))
		}
	}

	for _, name := range names {
		if r.MatchString(name) {
			s.log.V(2).Info(fmt.Sprintf("workload [%s] match workloadName [%s]", name, bkLogConfig.Spec.WorkloadName))
			return true
		}
		if name == bkLogConfig.Spec.WorkloadName {
			return true
		}
	}
	return false
}

func (s *BkLogSidecar) matchWorkloadType(bkLogConfig *v1alpha1.BkLogConfig, pod *corev1.Pod) bool {
	var kinds []string

	if utils.IsVclusterPod(pod) {
		kinds = append(kinds, utils.GetPodWorkloadType(pod, ""))
	} else {
		for _, ownerReference := range pod.GetOwnerReferences() {
			kinds = append(kinds, ownerReference.Kind)
		}
	}

	for _, kind := range kinds {
		if utils.ToLowerEq(kind, "ReplicaSet") {
			if utils.ToLowerEq(bkLogConfig.Spec.WorkloadType, "Deployment") {
				return true
			}
		}
		if utils.ToLowerEq(bkLogConfig.Spec.WorkloadType, kind) {
			return true
		}
	}
	s.log.V(2).Info(fmt.Sprintf("not match WorkloadType %s", bkLogConfig.Spec.WorkloadType))
	return false
}

func (s *BkLogSidecar) matchContainerName(containerName string, containerNameMatch []string, containerNameExclude []string) bool {
	// containerNameMatch empty return true because do not match containerName
	if len(containerNameExclude) != 0 {
		for _, excludeName := range containerNameExclude {
			if excludeName == containerName {
				// containerName is in containerNameExclude, return false
				s.log.V(2).Info(fmt.Sprintf("container name [%s] is in ExcludeNames, return", excludeName))
				return false
			}
		}
	}
	if len(containerNameMatch) == 0 {
		return true
	}
	for _, matchContainerName := range containerNameMatch {
		if matchContainerName == containerName {
			s.log.V(2).Info(fmt.Sprintf("container name [%s] match matchContainerName [%s]", containerName, matchContainerName))
			return true
		}
	}
	return false
}
