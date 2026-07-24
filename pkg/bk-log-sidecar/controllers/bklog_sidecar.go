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
	RuntimeOperationTimeout      = 10 * time.Second
	ConvergenceRetryBaseDelay    = time.Second
	ConvergenceRetryMaximumDelay = 30 * time.Second
)

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

// BkLogSidecar BkLogSidecar
type BkLogSidecar struct {
	runtime                   define.Runtime
	runtimeMu                 sync.Mutex
	kubeClient                client.Reader
	reloadAgentFn             func() error
	delayCleanFn              func(time.Duration, func())
	eventQueueMu              sync.Mutex
	eventQueue                workqueue.RateLimitingInterface
	eventWorkerOnce           sync.Once
	eventShutdownOnce         sync.Once
	lifecycleWG               sync.WaitGroup
	eventSequenceMu           sync.Mutex
	eventSequence             uint64
	latestEventSequence       map[string]uint64
	subscribeRetryInterval    time.Duration
	runtimeOperationTimeout   time.Duration
	convergenceRetryBaseDelay time.Duration
	convergenceRetryMaxDelay  time.Duration
	periodicReconcileInterval time.Duration
	periodicReconcileJitter   float64
	periodicReconcileDelayFn  func(time.Duration, float64) time.Duration
	// configMutationMu 只保护配置快照、磁盘事务与 reload 状态，不包围
	// Runtime/Kubernetes 查询；外部查询通过 configGeneration 做乐观校验。
	configMutationMu sync.Mutex
	configGeneration uint64
	reloadPending    bool
	// pendingContainerDeletes 只在 configMutationMu 保护下访问，用于保证容器退出后的
	// DelayCleanConfig 宽限期不会被并发的全量配置收敛提前裁剪。
	pendingContainerDeletes map[string]*pendingContainerDeletion
	pendingDeleteGeneration uint64
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
		// supervisor 已经在有效订阅期间完成首次全量扫描。
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
	s.log.Info(fmt.Sprintf("current node info is [%s], labels[%v]", node.Name, node.GetLabels()))
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

func (s *BkLogSidecar) generateActualBkLogConfigOnStartup() error {
	return s.generateActualBkLogConfigWithOptions(context.Background(), configGenerationOptions{forceReload: true})
}

func (s *BkLogSidecar) generateActualBkLogConfigForPeriodicReconcile(ctx context.Context) error {
	return s.generateActualBkLogConfigWithOptions(ctx, configGenerationOptions{refreshDiscoveredState: true})
}

func (s *BkLogSidecar) generateActualBkLogConfigForReconcile(
	namespace, name string,
	current *v1alpha1.BkLogConfig,
) error {
	return s.generateActualBkLogConfigWithOptions(context.Background(), configGenerationOptions{
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

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Build 阶段包含 Runtime/Kubernetes I/O，不能持有配置写锁。记录世代后
		// 在 Apply 前复核；若期间有事件提交了新快照，就基于最新状态重新 Build。
		generation := s.configSnapshotGeneration()
		logConfigs, err := s.buildActualBkLogConfigs(ctx, options)
		if err != nil {
			return err
		}
		desired, err := renderDesiredConfigs(logConfigs)
		if err != nil {
			return fmt.Errorf("render desired log configs: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		s.configMutationMu.Lock()
		if generation != s.configGeneration {
			s.configMutationMu.Unlock()
			continue
		}
		// Runtime 的全量列表只包含运行中容器。这里显式合并仍处于退出宽限期的配置，
		// 避免其他 BkLogConfig 的 reconcile 提前结束尾部日志采集。
		if err := s.preservePendingContainerConfigsLocked(desired, options.reconcile); err != nil {
			s.configMutationMu.Unlock()
			return fmt.Errorf("preserve pending container configs: %w", err)
		}
		err = s.applyDesiredConfigsLocked(desired, true, nil)
		s.configMutationMu.Unlock()
		return err
	}
}

// buildActualBkLogConfigs discovers the complete desired snapshot without
// mutating the active config cache or any on-disk file. Runtime/Node metadata
// caches may be refreshed during discovery.
func (s *BkLogSidecar) buildActualBkLogConfigs(
	ctx context.Context,
	options configGenerationOptions,
) ([]define.LogConfigType, error) {
	var logConfigs []define.LogConfigType
	var err error
	if options.refreshDiscoveredState {
		// 周期校准必须重新读取 Node；即使当前没有 node_log_config，也要避免
		// 后续匹配继续使用长期不更新的标签和 annotation。
		if err := s.refreshNodeInfoWithContext(ctx); err != nil {
			return nil, fmt.Errorf("refresh node info for periodic reconciliation: %w", err)
		}
	}
	// 一次 Build 只读取一份 BkLogConfig 快照，并同时用于所有容器和 Node。
	// 这样既避免按容器重复 DeepCopy 全量 informer cache，也不会在同一 desired
	// 中混入一次 CR 更新前后的两个版本。
	allBklogConfigs, err := s.bkLogConfigList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list BkLogConfigs for full configuration build: %w", err)
	}
	logConfigs, err = s.allContainerBkLogConfigs(
		ctx,
		logConfigs,
		options.refreshDiscoveredState,
		allBklogConfigs,
	)
	if err != nil {
		// An incomplete discovery result must never be treated as the desired
		// state, otherwise valid files could be deleted from a partial snapshot.
		return nil, fmt.Errorf("build container log configs: %w", err)
	}
	// match all node_log_config
	firstMatchNodeConfig := !options.refreshDiscoveredState
	for _, bkLogConfig := range allBklogConfigs {
		if !bkLogConfig.IsNodeType() {
			continue
		}
		if firstMatchNodeConfig {
			if err := s.refreshNodeInfoWithContext(ctx); err != nil {
				return nil, fmt.Errorf("refresh node info for node log config matching: %w", err)
			}
			firstMatchNodeConfig = false
		}
		node := s.currentNodeSnapshot()
		// label match
		if !s.matchLabel(bkLogConfig.Spec.LabelSelector, node.GetLabels()) {
			s.log.Info("current node not match label")
			continue
		}
		// annotation match
		if !s.matchAnnotation(bkLogConfig.Spec.AnnotationSelector, node.GetAnnotations()) {
			s.log.Info("current node not match annotation")
			continue
		}
		s.log.Info(fmt.Sprintf("[%s] log config match node[%s]", bkLogConfig.Name, node.Name))
		logConfigs = append(logConfigs, &define.NodeLogConfig{
			BkLogConfig: bkLogConfig,
			Node:        &node,
		})
	}

	if define.Empty(logConfigs) {
		s.log.Info("not have log config")
	}
	return logConfigs, nil
}

// allContainerBkLogConfigs will match all container log config (std and container log)
func (s *BkLogSidecar) allContainerBkLogConfigs(
	ctx context.Context,
	logConfigs []define.LogConfigType,
	refreshContainerInfo bool,
	bkLogConfigs []v1alpha1.BkLogConfig,
) ([]define.LogConfigType, error) {
	allContainer, err := s.allContainersWithContext(ctx)
	if err != nil {
		return logConfigs, fmt.Errorf("list runtime containers: %w", err)
	}
	for i, container := range allContainer {
		s.log.Info(fmt.Sprintf("container info -> [%d] [%s]", i, container.ID))
		c, ok := s.containerCache.Load(container.ID)
		if ok && !refreshContainerInfo {
			containerInfo := castContainer(c)
			logConfigs, err = s.containerBkLogConfigs(ctx, containerInfo, logConfigs, false, bkLogConfigs)
			if err != nil {
				return logConfigs, err
			}
			continue
		}
		// 周期校准不复用旧 containerCache，强制从 Runtime Inspect 当前状态；
		// 仍只做一次 Runtime List，避免“先刷新缓存、再全量 Build”的双重扫描。
		containerInfo, err := s.containerByIDWithContext(ctx, container.ID)
		if err != nil {
			return logConfigs, err
		}
		if containerInfo == nil {
			continue
		}
		s.containerCache.Store(container.ID, containerInfo)
		logConfigs, err = s.containerBkLogConfigs(ctx, containerInfo, logConfigs, false, bkLogConfigs)
		if err != nil {
			return logConfigs, err
		}
	}
	return logConfigs, nil
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

		if bkLogConfig.IsContainerType() {
			logConfigs = append(logConfigs, &define.ContainerLogConfig{
				BkLogConfig: bkLogConfig,
				Container:   container,
				Pod:         pod,
			})
			continue
		}

		runtime, err := s.getRuntimeWithContext(ctx)
		if err != nil {
			return logConfigs, fmt.Errorf("get runtime type for container %s: %w", container.ID, err)
		}
		logConfigs = append(logConfigs, &define.StdOutLogConfig{
			BkLogConfig: bkLogConfig,
			Container:   container,
			Pod:         pod,
			RuntimeType: runtime.Type(),
		})
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
	container, err := s.containerByIDWithContext(ctx, event.ContainerID)
	if err != nil {
		return fmt.Errorf("get container for create event %s: %w", event.ContainerID, err)
	}
	if container == nil {
		s.log.Info(fmt.Sprintf("container [%s] not exists, do nothing for action [%s].", event.ContainerID, event.Type))
		return nil
	}
	s.containerCache.Store(event.ContainerID, container)

	// 同一个容器 ID 可能在 stop 后再次收到 start（例如 runtime 事件重放）。
	// 只有确认新容器真实存在后才取消旧的延迟删除；否则一次过期 CREATE
	// 会让已经安排好的清理失效，并永久残留旧配置。
	s.cancelPendingContainerDeletion(event.ContainerID)

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
			if s.isConfigGenerationCurrent(generation) {
				return false, nil
			}
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
		if bkLogConfig.IsMatchBkEnv() {
			filteredConfigs = append(filteredConfigs, bkLogConfig)
		} else {
			s.log.Info(fmt.Sprintf("resource [%s] without label `%s=\"%s\"`, ignored",
				bkLogConfig.Name, config.BkEnvLabelName, config.BkEnv))
		}
	}
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
		s.log.Info("container is not k8s container")
		return matchBkLogConfigs, &pod, nil
	}

	s.log.Info(fmt.Sprintf("container name is [%s]", containerName))
	if utils.IsNetworkPod(containerName) {
		return matchBkLogConfigs, &pod, nil
	}

	for _, bkLogConfig := range bkLogConfigs {
		// only std and container log can match
		if !bkLogConfig.IsNeedMatchType() {
			continue
		}

		if !s.matchNamespace(&bkLogConfig, &pod) {
			s.log.Info(fmt.Sprintf("container name is [%s] not match namespace", containerName))
			continue
		}

		// if set all_container is true direct match
		if bkLogConfig.Spec.AllContainer {
			s.log.Info(fmt.Sprintf("[%s] log config match container [%s]", bkLogConfig.Name, containerName))
			matchBkLogConfigs = append(matchBkLogConfigs, bkLogConfig)
			continue
		}

		// label match
		if !s.matchLabel(bkLogConfig.Spec.LabelSelector, pod.GetLabels()) {
			s.log.Info(fmt.Sprintf("container name is [%s] not match label", containerName))
			continue
		}

		// annotation match
		if !s.matchAnnotation(bkLogConfig.Spec.AnnotationSelector, pod.GetAnnotations()) {
			s.log.Info(fmt.Sprintf("container name is [%s] not match annotation", containerName))
			continue
		}

		// match container by container_name
		if !s.matchContainerName(containerName, bkLogConfig.Spec.ContainerNameMatch, bkLogConfig.Spec.ContainerNameExclude) {
			s.log.Info(fmt.Sprintf("container name is [%s] not match container name", containerName))
			continue
		}

		// match pod by workload config
		if !s.matchWorkload(&bkLogConfig, &pod) {
			s.log.Info(fmt.Sprintf("container name is [%s] not match workload", containerName))
			continue
		}
		s.log.Info(fmt.Sprintf("[%s] log config match container [%s]", bkLogConfig.Name, containerName))
		matchBkLogConfigs = append(matchBkLogConfigs, bkLogConfig)
	}
	return matchBkLogConfigs, &pod, nil
}

func (s *BkLogSidecar) matchLabel(matchSelector metav1.LabelSelector, matchLabels map[string]string) bool {
	s.log.Info(fmt.Sprintf("selector: %v, labels %v", matchSelector, matchLabels))
	selector, err := metav1.LabelSelectorAsSelector(&matchSelector)
	if utils.NotNil(err) {
		s.log.Error(err, "selector to label selector failed")
		return false
	}
	labelSet := labels.Set(matchLabels)
	if !selector.Matches(labelSet) {
		return false
	}
	s.log.Info(fmt.Sprintf("label match success %v", matchSelector))
	return true
}

func (s *BkLogSidecar) matchAnnotation(matchSelector metav1.LabelSelector, matchAnnotations map[string]string) bool {
	s.log.Info(fmt.Sprintf("selector: %v, annotations %v", matchSelector, matchAnnotations))
	selector, err := metav1.LabelSelectorAsSelector(&matchSelector)
	if utils.NotNil(err) {
		s.log.Error(err, "selector to label selector failed")
		return false
	}
	annotationSet := labels.Set(matchAnnotations)
	if !selector.Matches(annotationSet) {
		return false
	}
	s.log.Info(fmt.Sprintf("annotation match success %v", matchSelector))
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
					s.log.Info(fmt.Sprintf("pod namespace [%s] match exclude namespace [%s]", pod.Namespace, namespace))
					return false
				}
			}
			return true
		} else if len(bkLogConfig.Spec.NamespaceSelector.MatchNames) != 0 {
			// 优先使用NamespaceSelector配置，列表中任意一个满足即可
			// 有一个匹配上则为true，否则直接false
			for _, namespace := range bkLogConfig.Spec.NamespaceSelector.MatchNames {
				if pod.Namespace == namespace {
					s.log.Info(fmt.Sprintf("pod namespace [%s] match namespace [%s]", pod.Namespace, namespace))
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
				s.log.Info(fmt.Sprintf("pod namespace [%s] match namespace [%s]", pod.Namespace, bkLogConfig.Spec.Namespace))
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
			s.log.Info(fmt.Sprintf("workload [%s] match workloadName [%s]", name, bkLogConfig.Spec.WorkloadName))
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
	s.log.Info(fmt.Sprintf("not match WorkloadType %s", bkLogConfig.Spec.WorkloadType))
	return false
}

func (s *BkLogSidecar) matchContainerName(containerName string, containerNameMatch []string, containerNameExclude []string) bool {
	// containerNameMatch empty return true because do not match containerName
	if len(containerNameExclude) != 0 {
		for _, excludeName := range containerNameExclude {
			if excludeName == containerName {
				// containerName is in containerNameExclude, return false
				s.log.Info(fmt.Sprintf("container name [%s] is in ExcludeNames, return", excludeName))
				return false
			}
		}
	}
	if len(containerNameMatch) == 0 {
		return true
	}
	for _, matchContainerName := range containerNameMatch {
		if matchContainerName == containerName {
			s.log.Info(fmt.Sprintf("container name [%s] match matchContainerName [%s]", containerName, matchContainerName))
			return true
		}
	}
	return false
}
