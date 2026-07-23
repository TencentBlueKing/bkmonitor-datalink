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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

const SubscribeRetryInterval = 5 * time.Second

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

// BkLogSidecar BkLogSidecar
type BkLogSidecar struct {
	runtime          define.Runtime
	kubeClient       client.Reader
	reloadAgentFn    func() error
	delayCleanFn     func(time.Duration, func())
	configMutationMu sync.Mutex
	reloadPending    bool
	// pendingContainerDeletes 只在 configMutationMu 保护下访问，用于保证容器退出后的
	// DelayCleanConfig 宽限期不会被并发的全量配置收敛提前裁剪。
	pendingContainerDeletes map[string]*pendingContainerDeletion
	pendingDeleteGeneration uint64
	containerCache          sync.Map
	currentNodeInfo         corev1.Node
	actualBkLogConfigCache  sync.Map
	log                     logr.Logger
	stopCh                  chan struct{}
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
		stopCh:                  make(chan struct{}),
		log:                     ctrl.Log.WithName("bkLogSidecar"),
		kubeClient:              mgr.GetCache(),
		pendingContainerDeletes: make(map[string]*pendingContainerDeletion),
	}
	return bkLogSidecar
}

// Start start bklog sidecar
func (s *BkLogSidecar) Start(_ context.Context) error {
	s.log.Info("start bklog sidecar")
	s.initContainerCache()
	s.initEventHandler()
	if err := s.generateActualBkLogConfigOnStartup(); err != nil {
		// Startup retry is handled by the later convergence stages. Keep the
		// runnable alive here so the CR reconciler can already recover failures.
		s.log.Error(err, "initial configuration generation failed")
	}
	return nil
}

// Stop stop bklog sidecar
func (s *BkLogSidecar) Stop() {
	s.log.Info("stop bklog sidecar")
	close(s.stopCh)
}

func (s *BkLogSidecar) getRuntime() define.Runtime {
	if s.runtime == nil {
		if err := s.refreshNodeInfo(); err != nil {
			s.log.Error(err, "refresh node info before runtime initialization failed")
		}
		s.runtime = NewRuntime(s.currentNodeInfo.Status.NodeInfo.ContainerRuntimeVersion)
	}
	return s.runtime

}

// initNodeInfo
func (s *BkLogSidecar) refreshNodeInfo() error {
	nodeName := os.Getenv(config.CurrentNodeNameKey)
	if !utils.StringNotEmpty(nodeName) {
		return fmt.Errorf("environment variable %s is empty", config.CurrentNodeNameKey)
	}
	err := s.kubeClient.Get(context.Background(), client.ObjectKey{
		Name: nodeName,
	}, &s.currentNodeInfo)
	if err != nil {
		return fmt.Errorf("get Node %s: %w", nodeName, err)
	}
	s.log.Info(fmt.Sprintf("current node info is [%s], labels[%v]", s.currentNodeInfo.Name, s.currentNodeInfo.GetLabels()))
	return nil
}

// initContainerCache init container cache
func (s *BkLogSidecar) initContainerCache() {
	if err := s.cacheContainer(); err != nil {
		s.log.Error(err, "initial container cache refresh failed")
	}
	go s.periodCacheContainer()
}

// initEventHandler init event handler
func (s *BkLogSidecar) initEventHandler() {
	go s.subscribeEvent()
}

// generateActualBkLogConfig will generate all actual bklog config
func (s *BkLogSidecar) generateActualBkLogConfig() error {
	return s.generateActualBkLogConfigWithOptions(configGenerationOptions{})
}

func (s *BkLogSidecar) generateActualBkLogConfigOnStartup() error {
	return s.generateActualBkLogConfigWithOptions(configGenerationOptions{forceReload: true})
}

func (s *BkLogSidecar) generateActualBkLogConfigForReconcile(
	namespace, name string,
	current *v1alpha1.BkLogConfig,
) error {
	return s.generateActualBkLogConfigWithOptions(configGenerationOptions{
		reconcile: &bkLogConfigReconcileState{
			key:     bkLogConfigKey{namespace: namespace, name: name},
			current: current,
		},
	})
}

func (s *BkLogSidecar) generateActualBkLogConfigWithOptions(options configGenerationOptions) error {
	// Hold the mutation lock across Build and Apply. Otherwise a container event
	// could update the live snapshot after discovery but before this full desired
	// state replaces it, causing the newer event to be lost.
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()
	if options.forceReload {
		// sidecar 重启会丢失内存中的 reloadPending。启动时先恢复该意图，
		// 即使首次 Build 失败，后续任一成功收敛也仍会补发 reload。
		s.reloadPending = true
	}

	logConfigs, err := s.buildActualBkLogConfigs()
	if err != nil {
		return err
	}
	desired, err := renderDesiredConfigs(logConfigs)
	if err != nil {
		return fmt.Errorf("render desired log configs: %w", err)
	}
	// Runtime 的全量列表只包含运行中容器。这里显式合并仍处于退出宽限期的配置，
	// 避免其他 BkLogConfig 的 reconcile 提前结束尾部日志采集。
	if err := s.preservePendingContainerConfigsLocked(desired, options.reconcile); err != nil {
		return fmt.Errorf("preserve pending container configs: %w", err)
	}
	return s.applyDesiredConfigsLocked(desired, true, nil)
}

// buildActualBkLogConfigs discovers the complete desired snapshot without
// mutating the in-memory cache or any on-disk file.
func (s *BkLogSidecar) buildActualBkLogConfigs() ([]define.LogConfigType, error) {
	var logConfigs []define.LogConfigType
	var err error
	logConfigs, err = s.allContainerBkLogConfigs(logConfigs)
	if err != nil {
		// An incomplete discovery result must never be treated as the desired
		// state, otherwise valid files could be deleted from a partial snapshot.
		return nil, fmt.Errorf("build container log configs: %w", err)
	}
	allBklogConfigs, err := s.bkLogConfigList()
	if err != nil {
		return nil, fmt.Errorf("list BkLogConfigs for node matching: %w", err)
	}
	// match all node_log_config
	firstMatchNodeConfig := true
	for _, bkLogConfig := range allBklogConfigs {
		if !bkLogConfig.IsNodeType() {
			continue
		}
		if firstMatchNodeConfig {
			if err := s.refreshNodeInfo(); err != nil {
				return nil, fmt.Errorf("refresh node info for node log config matching: %w", err)
			}
			firstMatchNodeConfig = false
		}
		// label match
		if !s.matchLabel(bkLogConfig.Spec.LabelSelector, s.currentNodeInfo.GetLabels()) {
			s.log.Info("current node not match label")
			continue
		}
		// annotation match
		if !s.matchAnnotation(bkLogConfig.Spec.AnnotationSelector, s.currentNodeInfo.GetAnnotations()) {
			s.log.Info("current node not match annotation")
			continue
		}
		s.log.Info(fmt.Sprintf("[%s] log config match node[%s]", bkLogConfig.Name, s.currentNodeInfo.Name))
		logConfigs = append(logConfigs, &define.NodeLogConfig{
			BkLogConfig: bkLogConfig,
			Node:        &s.currentNodeInfo,
		})
	}

	if define.Empty(logConfigs) {
		s.log.Info("not have log config")
	}
	return logConfigs, nil
}

// allContainerBkLogConfigs will match all container log config (std and container log)
func (s *BkLogSidecar) allContainerBkLogConfigs(logConfigs []define.LogConfigType) ([]define.LogConfigType, error) {
	allContainer, err := s.allContainers()
	if err != nil {
		return logConfigs, fmt.Errorf("list runtime containers: %w", err)
	}
	for i, container := range allContainer {
		s.log.Info(fmt.Sprintf("container info -> [%d] [%s]", i, container.ID))
		c, ok := s.containerCache.Load(container.ID)
		if ok {
			containerInfo := castContainer(c)
			logConfigs, err = s.containerBkLogConfigs(containerInfo, logConfigs, false)
			if err != nil {
				return logConfigs, err
			}
			continue
		}
		containerInfo, err := s.containerByID(container.ID)
		if err != nil {
			return logConfigs, err
		}
		if containerInfo == nil {
			continue
		}
		s.containerCache.Store(container.ID, containerInfo)
		logConfigs, err = s.containerBkLogConfigs(containerInfo, logConfigs, false)
		if err != nil {
			return logConfigs, err
		}
	}
	return logConfigs, nil
}

// containerBkLogConfigs will return single container all relation log config
func (s *BkLogSidecar) containerBkLogConfigs(container *define.Container, logConfigs []define.LogConfigType, isNewContainer bool) ([]define.LogConfigType, error) {
	matchBklogConfigs, pod, err := s.matchBklogConfigs(container)
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

		logConfigs = append(logConfigs, &define.StdOutLogConfig{
			BkLogConfig: bkLogConfig,
			Container:   container,
			Pod:         pod,
			RuntimeType: s.getRuntime().Type(),
		})
	}
	return logConfigs, nil
}

// allContainers will all container info
func (s *BkLogSidecar) allContainers() ([]define.SimpleContainer, error) {
	ctx := context.Background()
	return s.getRuntime().Containers(ctx)
}

// subscribeEvent is init listen event handler then handler event
func (s *BkLogSidecar) subscribeEvent() {
	ctx, cancel := context.WithCancel(context.Background())

	events, errs := s.getRuntime().Subscribe(ctx)

	go func() {
		for {
			select {
			case event := <-events:
				s.eventHandler(event)
			case err := <-errs:
				s.log.Info(fmt.Sprintf("runtime subscribe got error: %s, will retry in %s", err, SubscribeRetryInterval.String()))
				cancel()
				time.Sleep(SubscribeRetryInterval)
				s.subscribeEvent()
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// eventHandler handler event
func (s *BkLogSidecar) eventHandler(event *define.ContainerEvent) {
	switch event.Type {
	case define.ContainerEventCreate:
		s.startActionHandler(event)
	case define.ContainerEventDelete:
		s.destroyActionHandler(event)
	case define.ContainerEventStop:
		s.stopActionHandler(event)
	default:
		s.log.Info(fmt.Sprintf("not expecting event type [%s] for container [%s]", event.Type, event.ContainerID))
		return
	}
	//}
}

// startActionHandler handler start event
func (s *BkLogSidecar) startActionHandler(event *define.ContainerEvent) {
	s.log.Info(fmt.Sprintf("start handler [%s] for container [%s]", event.Type, event.ContainerID))
	// 同一个容器 ID 可能在 stop 后再次收到 start（例如 runtime 事件重放）。
	// 先取消旧的延迟删除，防止旧定时任务在新配置写入后将其误删。
	s.cancelPendingContainerDeletion(event.ContainerID)

	container, err := s.getContainerInfoByID(event.ContainerID)
	if err != nil {
		s.log.Error(err, "get container for create event failed", "containerID", event.ContainerID)
		return
	}
	if container == nil {
		s.log.Info(fmt.Sprintf("container [%s] not exists, do nothing for action [%s].", event.ContainerID, event.Type))
		return
	}

	var bkLogConfigs []define.LogConfigType
	bkLogConfigs, err = s.containerBkLogConfigs(container, bkLogConfigs, true)
	if err != nil {
		s.log.Error(err, "build configs for create event failed", "containerID", event.ContainerID)
		return
	}
	if define.Empty(bkLogConfigs) {
		s.log.Info(fmt.Sprintf("container [%s] not match log config", container.ID))
		return
	}
	if err := s.upsertActualConfigs(bkLogConfigs); err != nil {
		s.log.Error(err, "apply configs for create event failed", "containerID", event.ContainerID)
		return
	}
	s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
}

// destroyActionHandler handler destroy event
func (s *BkLogSidecar) destroyActionHandler(event *define.ContainerEvent) {
	s.log.Info(fmt.Sprintf("start handler [%s] for container [%s]", event.Type, event.ContainerID))
	containerInfo, ok := s.containerCache.Load(event.ContainerID)
	if ok {
		s.scheduleContainerConfigDeletion(castContainer(containerInfo), true)
	}
	s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
}

// stopActionHandler handler stop event
func (s *BkLogSidecar) stopActionHandler(event *define.ContainerEvent) {
	s.log.Info(fmt.Sprintf("start handler [%s] for container [%s]", event.Type, event.ContainerID))

	go func(containerId string) {
		container, err := s.getContainerInfoByID(containerId)
		if err != nil {
			s.log.Error(err, "get container for stop event failed", "containerID", containerId)
			return
		}
		if container == nil {
			s.log.Info(fmt.Sprintf("container [%s] not exists, do nothing for action [%s].", event.ContainerID, event.Type))
			return
		}

		s.scheduleContainerConfigDeletion(container, false)
		s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
	}(event.ContainerID)
}

// bkLogConfigList will get all BkLogConfig from k8s
func (s *BkLogSidecar) bkLogConfigList() ([]v1alpha1.BkLogConfig, error) {
	var bkLogConfigs v1alpha1.BkLogConfigList
	err := s.kubeClient.List(context.Background(), &bkLogConfigs)
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
func (s *BkLogSidecar) matchBklogConfigs(container *define.Container) ([]v1alpha1.BkLogConfig, *corev1.Pod, error) {
	matchBkLogConfigs := make([]v1alpha1.BkLogConfig, 0)
	var pod corev1.Pod
	err := s.kubeClient.Get(context.Background(), client.ObjectKey{
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

	bkLogConfigs, err := s.bkLogConfigList()
	if err != nil {
		return matchBkLogConfigs, &pod, fmt.Errorf("list BkLogConfigs: %w", err)
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
