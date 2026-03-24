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
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const SubscribeRetryInterval = 5 * time.Second

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

// BkLogSidecar BkLogSidecar
type BkLogSidecar struct {
	sync.RWMutex
	runtime                define.Runtime
	kubeClient             cache.Cache
	containerCache         sync.Map
	currentNodeInfo        corev1.Node
	actualBkLogConfigCache sync.Map
	log                    logr.Logger
	stopCh                 chan struct{}
}

// NewBkLogSidecar new BkLogSidecar
func NewBkLogSidecar(mgr ctrl.Manager) *BkLogSidecar {
	bkLogSidecar := &BkLogSidecar{
		stopCh:     make(chan struct{}),
		log:        ctrl.Log.WithName("bkLogSidecar"),
		kubeClient: mgr.GetCache(),
	}
	return bkLogSidecar
}

// Start start bklog sidecar
func (s *BkLogSidecar) Start(_ context.Context) error {
	s.log.Info("start bklog sidecar")
	s.initContainerCache()
	s.initEventHandler()
	s.generateActualBkLogConfig()
	return nil
}

// Stop stop bklog sidecar
func (s *BkLogSidecar) Stop() {
	s.log.Info("stop bklog sidecar")
	close(s.stopCh)
}

func (s *BkLogSidecar) getRuntime() define.Runtime {
	if s.runtime == nil {
		s.refreshNodeInfo()
		s.runtime = NewRuntime(s.currentNodeInfo.Status.NodeInfo.ContainerRuntimeVersion)
	}
	return s.runtime

}

// initNodeInfo
func (s *BkLogSidecar) refreshNodeInfo() {
	nodeName := os.Getenv(config.CurrentNodeNameKey)
	if !utils.StringNotEmpty(nodeName) {
		s.log.Info("not set up node name env")
		return
	}
	err := s.kubeClient.Get(context.Background(), client.ObjectKey{
		Name: nodeName,
	}, &s.currentNodeInfo)
	if utils.NotNil(err) {
		s.log.Error(err, fmt.Sprintf("failed to get node[%s] info", nodeName))
	}
	s.log.Info(fmt.Sprintf("current node info is [%s], labels[%v]", s.currentNodeInfo.Name, s.currentNodeInfo.GetLabels()))
}

// initContainerCache init container cache
func (s *BkLogSidecar) initContainerCache() {
	s.cacheContainer()
	go s.periodCacheContainer()
}

// initEventHandler init event handler
func (s *BkLogSidecar) initEventHandler() {
	go s.subscribeEvent()
}

// generateActualBkLogConfig will generate all actual bklog config
func (s *BkLogSidecar) generateActualBkLogConfig() {
	var logConfigs []define.LogConfigType
	logConfigs = s.allContainerBkLogConfigs(logConfigs)
	allBklogConfigs, err := s.bkLogConfigList()
	utils.CheckErrorFn(err, func(err error) {
		s.log.Error(err, "get bkLogConfigList failed")
	})
	if utils.IsNil(err) {
		// match all node_log_config
		firstMatchNodeConfig := true
		for _, bkLogConfig := range allBklogConfigs {
			if !bkLogConfig.IsNodeType() {
				continue
			}
			if firstMatchNodeConfig {
				s.refreshNodeInfo()
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
	}

	if define.Empty(logConfigs) {
		s.log.Info("not have log config")
	}

	for _, logConfig := range logConfigs {
		s.actualBkLogConfigCache.Store(logConfig.ConfigName(), logConfig)
	}
	s.deleteInvalidConfig()
	s.writeConfig()
	utils.CheckErrorFn(s.reloadBkunifylogbeat(), func(err error) {
		s.log.Error(err, "generate bkLogConfig then reload agent failed")
	})
}

// allContainerBkLogConfigs will match all container log config (std and container log)
func (s *BkLogSidecar) allContainerBkLogConfigs(logConfigs []define.LogConfigType) []define.LogConfigType {
	allContainer, err := s.allContainers()
	if utils.NotNil(err) {
		s.log.Error(err, "get all containers failed")
		return logConfigs
	}
	for i, container := range allContainer {
		s.log.Info(fmt.Sprintf("container info -> [%d] [%s]", i, container.ID))
		c, ok := s.containerCache.Load(container.ID)
		if ok {
			containerInfo := castContainer(c)
			logConfigs = s.containerBkLogConfigs(containerInfo, logConfigs, false)
			continue
		}
		containerInfo := s.containerByID(container.ID)
		if containerInfo == nil {
			s.log.Error(fmt.Errorf("get container info %s failed", container.ID), "")
			continue
		}
		s.containerCache.Store(container.ID, containerInfo)
		logConfigs = s.containerBkLogConfigs(containerInfo, logConfigs, false)
	}
	return logConfigs
}

// containerBkLogConfigs will return single container all relation log config
func (s *BkLogSidecar) containerBkLogConfigs(container *define.Container, logConfigs []define.LogConfigType, isNewContainer bool) []define.LogConfigType {
	matchBklogConfigs, pod := s.matchBklogConfigs(container)
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
	return logConfigs
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

	container := s.getContainerInfoByID(event.ContainerID)
	if container == nil {
		s.log.Info(fmt.Sprintf("container [%s] not exists, do nothing for action [%s].", event.ContainerID, event.Type))
		return
	}

	var bkLogConfigs []define.LogConfigType
	bkLogConfigs = s.containerBkLogConfigs(container, bkLogConfigs, true)
	if define.Empty(bkLogConfigs) {
		s.log.Info(fmt.Sprintf("container [%s] not match log config", container.ID))
		return
	}
	for _, logConfig := range bkLogConfigs {
		s.actualBkLogConfigCache.Store(logConfig.ConfigName(), logConfig)
	}
	s.writeConfig()
	utils.CheckErrorFn(s.reloadBkunifylogbeat(), func(err error) {
		s.log.Error(err, "handler event reload agent failed")
	})
	s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
}

// destroyActionHandler handler destroy event
func (s *BkLogSidecar) destroyActionHandler(event *define.ContainerEvent) {
	s.log.Info(fmt.Sprintf("start handler [%s] for container [%s]", event.Type, event.ContainerID))
	go func(containerId string) {
		containerInfo, ok := s.containerCache.Load(containerId)
		if ok {
			utils.AfterForFn(time.Duration(config.DelayCleanConfig)*time.Second, func() {
				s.containerCache.Delete(containerId)
				if s.deleteContainerConfig(castContainer(containerInfo)) {
					utils.CheckErrorFn(s.reloadBkunifylogbeat(), func(err error) {
						s.log.Error(err, "handler event reload agent failed")
					})
				}
			})
		}
		s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
	}(event.ContainerID)
}

// stopActionHandler handler stop event
func (s *BkLogSidecar) stopActionHandler(event *define.ContainerEvent) {
	s.log.Info(fmt.Sprintf("start handler [%s] for container [%s]", event.Type, event.ContainerID))

	go func(containerId string) {
		container := s.getContainerInfoByID(containerId)
		if container == nil {
			s.log.Info(fmt.Sprintf("container [%s] not exists, do nothing for action [%s].", event.ContainerID, event.Type))
			return
		}

		utils.AfterForFn(time.Duration(config.DelayCleanConfig)*time.Second, func() {
			if s.deleteContainerConfig(container) {
				utils.CheckErrorFn(s.reloadBkunifylogbeat(), func(err error) {
					s.log.Error(err, "handler event reload agent failed")
				})
			}
		})
		s.log.Info(fmt.Sprintf("end handler [%s] for container [%s] done", event.Type, event.ContainerID))
	}(event.ContainerID)
}

// deleteConfigByName will by BkLogConfig name to delete all relation actual log config
func (s *BkLogSidecar) deleteConfigByName(namespace, name string) {
	namespacedName := fmt.Sprintf("%s_%s", namespace, name)
	s.log.Info(fmt.Sprintf("delete config [%s]", namespacedName))
	s.actualBkLogConfigCache.Range(func(key, value interface{}) bool {
		configKey := key.(string)
		logConfig := value.(define.LogConfigType)
		if strings.HasSuffix(configKey, namespacedName) {
			s.log.Info(fmt.Sprintf("config [%s] match -> [%s], so will delete", configKey, namespacedName))
			s.deleteConfigFile(logConfig)
			s.actualBkLogConfigCache.Delete(key)
		}
		return true
	})
}

// deleteContainerConfig will delete all log config for container
func (s *BkLogSidecar) deleteContainerConfig(container *define.Container) bool {
	s.log.Info(fmt.Sprintf("delete config for container [%s]", container.ID))
	canReload := false
	s.actualBkLogConfigCache.Range(func(key, value interface{}) bool {
		configKey := key.(string)
		if strings.HasPrefix(configKey, container.ID) {
			s.deleteConfigFile(value.(define.LogConfigType))
			s.actualBkLogConfigCache.Delete(configKey)
			canReload = true
		}
		return true
	})
	s.log.Info(fmt.Sprintf("delete container config [%s] complete", container.ID))
	return canReload
}

// deleteConfig delete all config
func (s *BkLogSidecar) deleteConfig() {
	s.actualBkLogConfigCache.Range(func(key, logConfig interface{}) bool {
		s.deleteConfigFile(logConfig.(define.LogConfigType))
		s.actualBkLogConfigCache.Delete(key)
		return true
	})
}

// deleteInValidConfig delete not in sidecar cache config
func (s *BkLogSidecar) deleteInvalidConfig() {
	s.log.Info("delete invalid config")
	files, err := ioutil.ReadDir(config.BkunifylogbeatConfig)
	if utils.NotNil(err) {
		s.log.Error(err, fmt.Sprintf("read dir %s failed", config.BkunifylogbeatConfig))
		return
	}
	for _, file := range files {
		confKey := strings.ReplaceAll(file.Name(), ".conf", "")
		_, ok := s.actualBkLogConfigCache.Load(confKey)
		if ok {
			s.log.Info(fmt.Sprintf("config [%s] is valid", confKey))
			continue
		}
		err := os.Remove(filepath.Join(config.BkunifylogbeatConfig, file.Name()))
		if utils.NotNil(err) {
			s.log.Error(err, fmt.Sprintf("remove config file [%s] failed", file.Name()))
			continue
		}
		s.log.Info(fmt.Sprintf("delete invalid file [%s] success", file.Name()))
	}
	s.log.Info("delete invalid complete")
}

// writeConfig write config to local
func (s *BkLogSidecar) writeConfig() {
	s.actualBkLogConfigCache.Range(func(_, logConfigInterface interface{}) bool {
		logConfig := logConfigInterface.(define.LogConfigType)
		s.writeConfigFile(logConfig)
		return true
	})
}

// writeConfigFile will write log config to file
func (s *BkLogSidecar) writeConfigFile(logConfig define.LogConfigType) {
	configPath := fmt.Sprintf("%s.conf", filepath.Join(config.BkunifylogbeatConfig, logConfig.ConfigName()))
	fd, err := os.Create(configPath)
	if utils.NotNil(err) {
		s.log.Error(err, fmt.Sprintf("create config file [%s] failed", configPath))
		return
	}
	defer fd.Close()
	_, err = fd.Write(logConfig.Config())
	if utils.NotNil(err) {
		s.log.Error(err, fmt.Sprintf("write config file [%s] failed", configPath))
		return
	}
	s.log.Info(fmt.Sprintf("config file [%s] has write success", configPath))
}

// deleteConfigFile by logConfig to delete actual log config file
func (s *BkLogSidecar) deleteConfigFile(logConfig define.LogConfigType) {
	configPath := fmt.Sprintf("%s.conf", filepath.Join(config.BkunifylogbeatConfig, logConfig.ConfigName()))
	err := os.Remove(configPath)
	if utils.NotNil(err) {
		s.log.Error(err, fmt.Sprintf("remove config file [%s] failed", configPath))
		return
	}
	s.log.Info(fmt.Sprintf("remove config file [%s] success", configPath))
}

// bkLogConfigList will get all BkLogConfig from k8s
func (s *BkLogSidecar) bkLogConfigList() ([]v1alpha1.BkLogConfig, error) {
	var bkLogConfigs v1alpha1.BkLogConfigList
	err := s.kubeClient.List(context.Background(), &bkLogConfigs)

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
func (s *BkLogSidecar) matchBklogConfigs(container *define.Container) ([]v1alpha1.BkLogConfig, *corev1.Pod) {
	matchBkLogConfigs := make([]v1alpha1.BkLogConfig, 0)
	var pod corev1.Pod
	err := s.kubeClient.Get(context.Background(), client.ObjectKey{
		Namespace: container.Labels[config.ContainerLabelK8sPodNamespace],
		Name:      container.Labels[config.ContainerLabelK8sPodName],
	}, &pod)

	if utils.NotNil(err) {
		s.log.Error(err, "get container pod info failed")
		return matchBkLogConfigs, &pod
	}

	bkLogConfigs, err := s.bkLogConfigList()
	if utils.NotNil(err) {
		s.log.Error(err, "get bkLogConfig failed")
		return matchBkLogConfigs, &pod
	}

	containerName, ok := container.Labels[config.ContainerLabelK8sContainerName]
	if !ok {
		s.log.Info("container is not k8s container")
		return matchBkLogConfigs, &pod
	}

	s.log.Info(fmt.Sprintf("container name is [%s]", containerName))
	if utils.IsNetworkPod(containerName) {
		return matchBkLogConfigs, &pod
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
	return matchBkLogConfigs, &pod
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
