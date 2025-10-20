// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"math"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus-operator/prometheus-operator/pkg/informers"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	statefulSetWorkerLabel = "app.kubernetes.io/component=bkmonitorbeat-statefulset"
	statefulSetSecretLabel = "taskType=statefulset"
	statefulSetWorkerName  = "bkm-statefulset-worker"

	defaultStatefulSetWorkerFactor = 600
)

func (c *Operator) listWatchStatefulSetWorker() error {
	informer, err := informers.NewInformersForResource(
		informers.NewKubeInformerFactories(
			map[string]struct{}{configs.G().MonitorNamespace: {}},
			nil,
			c.client,
			define.ReSyncPeriod,
			func(options *metav1.ListOptions) {
				options.LabelSelector = statefulSetWorkerLabel
			},
		),
		appsv1.SchemeGroupVersion.WithResource("statefulsets"),
	)
	if err != nil {
		return errors.Wrap(err, "create StatefulSet informer failed")
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.handleStatefulSetWorkerAdd,
		UpdateFunc: c.handleStatefulSetWorkerUpdate,
		DeleteFunc: c.handleStatefulSetWorkerDelete,
	})
	go informer.Start(c.ctx.Done())

	for _, inf := range informer.GetInformers() {
		if !k8sutils.WaitForNamedCacheSync(c.ctx, "StatefulSet", inf.Informer()) {
			return errors.New("failed to sync StatefulSet caches")
		}
	}
	return nil
}

func (c *Operator) handleStatefulSetWorkerAdd(obj any) {
	statefulset, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		logger.Errorf("expected StatefulSet type, got %T", obj)
		return
	}

	replica := statefulset.Spec.Replicas
	if replica != nil {
		c.statefulSetWorker = int(*replica)
		discover.Publish()
	}
}

func (c *Operator) handleStatefulSetWorkerDelete(obj any) {
	_, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		logger.Errorf("expected StatefulSet type, got %T", obj)
		return
	}
	c.statefulSetWorker = 0
	discover.Publish()
}

func (c *Operator) handleStatefulSetWorkerUpdate(oldObj, newObj any) {
	old, ok := oldObj.(*appsv1.StatefulSet)
	if !ok {
		logger.Errorf("expected StatefulSet type, got %T", oldObj)
		return
	}
	cur, ok := newObj.(*appsv1.StatefulSet)
	if !ok {
		logger.Errorf("expected StatefulSet type, got %T", newObj)
		return
	}

	if old.ResourceVersion == cur.ResourceVersion {
		logger.Debugf("StatefulSet '%s/%s' does not change", old.Namespace, old.Name)
		return
	}

	replica := cur.Spec.Replicas
	if replica != nil {
		c.statefulSetWorker = int(*replica)
		discover.Publish()
	}
}

func (c *Operator) listWatchStatefulSetSecrets() error {
	informer, err := informers.NewInformersForResource(
		informers.NewKubeInformerFactories(
			map[string]struct{}{configs.G().MonitorNamespace: {}},
			nil,
			c.client,
			define.ReSyncPeriod,
			func(options *metav1.ListOptions) {
				options.LabelSelector = statefulSetSecretLabel
			},
		),
		corev1.SchemeGroupVersion.WithResource(string(corev1.ResourceSecrets)),
	)
	if err != nil {
		return errors.Wrap(err, "create statefulset secret informer failed")
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.handleStatefulSetSecretAdd,
		UpdateFunc: c.handleStatefulSetSecretUpdate,
		DeleteFunc: c.handleStatefulSetSecretDelete,
	})
	go informer.Start(c.ctx.Done())

	for _, inf := range informer.GetInformers() {
		if !k8sutils.WaitForNamedCacheSync(c.ctx, "Secret", inf.Informer()) {
			return errors.New("failed to sync Secret caches")
		}
	}
	return nil
}

func (c *Operator) handleStatefulSetSecretAdd(obj any) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		logger.Errorf("expected Secret type, got %T", obj)
		return
	}

	c.statefulSetSecretMut.Lock()
	defer c.statefulSetSecretMut.Unlock()
	c.statefulSetSecretMap[secret.Name] = struct{}{}
}

func (c *Operator) handleStatefulSetSecretDelete(obj any) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		logger.Errorf("expected Secret type, got %T", obj)
		return
	}

	c.statefulSetSecretMut.Lock()
	defer c.statefulSetSecretMut.Unlock()
	delete(c.statefulSetSecretMap, secret.Name)
}

// 无需关心 update 事件
func (c *Operator) handleStatefulSetSecretUpdate(oldObj, newObj any) {}

// reconcileStatefulSetWorker 对 statefulset worker 进行扩缩容
func (c *Operator) reconcileStatefulSetWorker(configCount int) {
	n := calcShouldStatefulSetWorker(configCount)
	logger.Infof("statefulset workers count should be %d, childConfigs count: %d", n, configCount)
	c.mm.SetStatefulSetWorkerCount(n)

	// 2 分钟内最多只允许调度 1 次
	if time.Now().Unix()-c.statefulSetWorkerScaled.Unix() < 120 {
		return
	}

	// 期待的 workers 没有变化 不做任何处理
	if c.statefulSetWorker == n {
		return
	}

	statefulsetClient := c.client.AppsV1().StatefulSets(configs.G().MonitorNamespace)
	c.statefulSetWorkerScaled = time.Now()
	scale, err := statefulsetClient.GetScale(c.ctx, statefulSetWorkerName, metav1.GetOptions{})
	if err != nil {
		logger.Errorf("failed to get statefulset worker scale: %v", err)
		c.mm.IncScaledStatefulSetFailedCounter()
		return
	}
	sc := *scale
	sc.Spec.Replicas = int32(n)

	_, err = statefulsetClient.UpdateScale(c.ctx, statefulSetWorkerName, &sc, metav1.UpdateOptions{})
	if err != nil {
		logger.Errorf("failed to scale statefulset worker replicas from %d to %d: %v", c.statefulSetWorker, n, err)
		c.mm.IncScaledStatefulSetFailedCounter()
		return
	}
	logger.Infof("scale statefulset worker replicas from %d to %d", c.statefulSetWorker, n)
	c.mm.IncScaledStatefulSetSuccessCounter()

	// 尽力确保 statefulset worker 已经扩容完成再进行任务调度
	// 避免影响到原有的数据采集（但此操作会卡住 operator 的调度流程）
	maxRetry := configs.G().StatefulSetWorkerScaleMaxRetry
	if maxRetry <= 0 {
		maxRetry = 12 // 1min
	}

	start := time.Now()
	for i := 0; i < maxRetry; i++ {
		time.Sleep(notifier.WaitPeriod)
		statefulset, err := statefulsetClient.Get(c.ctx, statefulSetWorkerName, metav1.GetOptions{})
		if err != nil {
			logger.Errorf("failed to get statefulset worker: %v", err)
			return
		}

		// 扩容完成
		if int(statefulset.Status.ReadyReplicas) == n {
			logger.Infof("scacle statefulset worker finished, take %s", time.Since(start))
			return
		}
		logger.Infof("waiting for statefulset operation, round: %d", i+1)
	}
}

// calcShouldStatefulSetWorker 根据采集任务数量计算需要 worker 个数 算法需要四个配置项参与
//
// ConfStatefulSetWorkerHpa: 是否需要开启 HPA
// ConfStatefulSetReplicas: 默认最小 worker 数量
// ConfStatefulSetMaxReplicas: 最大 worker 数量
// ConfStatefulSetWorkerFactor: 每个 worker 最多采集的任务数量（近似值）
func calcShouldStatefulSetWorker(n int) int {
	// 判断是否需要开启 HPA 如果不开启的话使用 ConfStatefulSetReplicas
	// 如果采集任务数量为 0 也保证最少有 ConfStatefulSetReplicas 个 worker
	if !configs.G().StatefulSetWorkerHpa || n <= 0 {
		if configs.G().StatefulSetReplicas <= 0 {
			return 1
		}
		return configs.G().StatefulSetReplicas
	}

	// 如果开启 HPA 的话 需要先检查每个 worker 最多允许多少个采集任务
	factor := configs.G().StatefulSetWorkerFactor
	if factor <= 0 {
		factor = defaultStatefulSetWorkerFactor
	}

	// 按采集数量分配 计算出总共需要多少 workers 四舍五入
	expectedWorkers := int(math.Round(float64(n) / float64(factor)))
	// 确保 worker 数量不能超过 ConfStatefulSetMaxReplicas
	if expectedWorkers >= configs.G().StatefulSetMaxReplicas {
		expectedWorkers = configs.G().StatefulSetMaxReplicas
	}

	// 保证最少有一个 worker
	if expectedWorkers <= 0 {
		expectedWorkers = 1
	}

	// 保证 workers 数量不低于 ConfStatefulSetReplicas
	if configs.G().StatefulSetReplicas > expectedWorkers {
		expectedWorkers = configs.G().StatefulSetReplicas
	}

	return expectedWorkers
}
