// Copyright 2022 The prometheus-operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package qcloudmonitor

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promcli "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	prominfs "github.com/prometheus-operator/prometheus-operator/pkg/informers"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/utils/pointer"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	bkcli "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// 避免与 Helm 内置 `app.kubernetes.io/managed-by` 冲突 使用了 `monitoring.bk.tencent.com/managed-by`
const (
	labelAppManagedBy = "monitoring.bk.tencent.com/managed-by"
	labelAppInstance  = "monitoring.bk.tencent.com/instance"

	appLabelSelection = labelAppManagedBy + "=" + define.AppName
)

// Operator 需要确保 QCloudMonitor 所关联的资源的一致性
// - ConfigMap
// - Service
// - ServiceMonitor
// - Deployment
//
// 判断是否关联的条件
// 1) 资源与 QCloudMonitor 处于相同的 namespace <Required>
// 2) 资源与 QCloudMonitor 同名 <Required>
// 3) 资源存在 `monitoring.bk.tencent.com/managed-by: bkmonitor-operator` Labels
// 4) 资源存在 `metav1.OwnerReference` 且归属于同名 QCloudMonitor
//
// Namespace/Name 优先级会高与 3/4 规则，即同名资源会被 Operator 强制修正，如果是 Name 发生变更
// 则 Operator 会将其视作脱离管控。
//
// 所有关联资源`只能`通过 QCloudMonitor 进行配置，即 QCloudMonitor 资源是唯一的变更入口
// - 当 Operator 收到关联资源的变更（Add/Update/Delete）事件时，只需溯源到对应的 QCloudMonitor 资源并执行一次 CreateOrUpdate 即可
// - 当 Operator 收到 QCloudMonitor 的 Add/Update 事件时，执行 CreateOrUpdate
// - 当 Operator 收到 QCloudMonitor 的 Delete 事件时，会删除 QCloudMonitor 关联的所有资源（OwnerRef）
type Operator struct {
	ctx    context.Context
	cancel context.CancelFunc

	client  kubernetes.Interface
	metaCli metadata.Interface
	bkCli   bkcli.Interface
	promCli promcli.Interface

	seh *syncEventHandler

	qcmInfs *prominfs.ForResource // QCloudMonitor
	dpInfs  *prominfs.ForResource // Deployment
	svcInfs *prominfs.ForResource // Service
	cmInfs  *prominfs.ForResource // ConfigMap
	smInfs  *prominfs.ForResource // ServiceMonitor
}

type ClientSet struct {
	Client kubernetes.Interface
	Meta   metadata.Interface
	BK     bkcli.Interface
	Prom   promcli.Interface
}

func New(ctx context.Context, cs ClientSet) (*Operator, error) {
	var (
		operator = new(Operator)
		err      error
	)

	operator.ctx, operator.cancel = context.WithCancel(ctx)
	operator.client = cs.Client
	operator.metaCli = cs.Meta
	operator.promCli = cs.Prom
	operator.bkCli = cs.BK

	allNamespaces := map[string]struct{}{}
	if len(configs.G().QCloudMonitor.TargetNamespaces) == 0 {
		allNamespaces = map[string]struct{}{corev1.NamespaceAll: {}}
	} else {
		for _, namespace := range configs.G().QCloudMonitor.TargetNamespaces {
			allNamespaces[namespace] = struct{}{}
		}
	}

	denyTargetNamespaces := make(map[string]struct{})
	for _, namespace := range configs.G().QCloudMonitor.DenyTargetNamespaces {
		denyTargetNamespaces[namespace] = struct{}{}
	}

	operator.smInfs, err = prominfs.NewInformersForResource(
		prominfs.NewMonitoringInformerFactories(
			allNamespaces,
			denyTargetNamespaces,
			operator.promCli,
			define.ReSyncPeriod,
			func(options *metav1.ListOptions) {
				options.LabelSelector = appLabelSelection
			},
		),
		promv1.SchemeGroupVersion.WithResource(promv1.ServiceMonitorName),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create ServiceMonitor informer failed")
	}

	operator.svcInfs, err = prominfs.NewInformersForResource(
		prominfs.NewKubeInformerFactories(
			allNamespaces,
			denyTargetNamespaces,
			operator.client,
			define.ReSyncPeriod,
			func(options *metav1.ListOptions) {
				options.LabelSelector = appLabelSelection
			},
		),
		corev1.SchemeGroupVersion.WithResource("services"),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create Service informer failed")
	}

	operator.cmInfs, err = prominfs.NewInformersForResource(
		prominfs.NewKubeInformerFactories(
			allNamespaces,
			denyTargetNamespaces,
			operator.client,
			define.ReSyncPeriod,
			func(options *metav1.ListOptions) {
				options.LabelSelector = appLabelSelection
			},
		),
		corev1.SchemeGroupVersion.WithResource("configmaps"),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create ConfigMap informer failed")
	}

	operator.dpInfs, err = prominfs.NewInformersForResource(
		prominfs.NewKubeInformerFactories(
			allNamespaces,
			denyTargetNamespaces,
			operator.client,
			define.ReSyncPeriod,
			func(options *metav1.ListOptions) {
				options.LabelSelector = appLabelSelection
			},
		),
		appsv1.SchemeGroupVersion.WithResource("deployments"),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create Deployment informer failed")
	}

	operator.qcmInfs, err = prominfs.NewInformersForResource(
		k8sutils.NewBKInformerFactories(
			allNamespaces,
			denyTargetNamespaces,
			operator.bkCli,
			define.ReSyncPeriod,
			nil,
		),
		bkv1beta1.SchemeGroupVersion.WithResource("qcloudmonitors"),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create QCloudMonitor informer failed")
	}

	operator.seh = newSyncEventHandler(operator)
	return operator, nil
}

func (c *Operator) Start() error {
	c.seh.Run(c.ctx)

	startInfs := func(infs *prominfs.ForResource) {
		infs.AddEventHandler(c.seh)
		infs.Start(c.ctx.Done())
	}

	startInfs(c.qcmInfs)
	startInfs(c.svcInfs)
	startInfs(c.smInfs)
	startInfs(c.dpInfs)
	startInfs(c.cmInfs)

	return c.waitForCacheSync()
}

func (c *Operator) Stop() {
	c.cancel()
}

func (c *Operator) waitForCacheSync() error {
	for _, infs := range []struct {
		name                 string
		informersForResource *prominfs.ForResource
	}{
		{"QCloudMonitor", c.qcmInfs},
		{"Service", c.svcInfs},
		{"Deployment", c.dpInfs},
		{"ConfigMap", c.cmInfs},
		{"ServiceMonitor", c.smInfs},
	} {
		if infs.informersForResource == nil {
			continue
		}

		for _, inf := range infs.informersForResource.GetInformers() {
			if !k8sutils.WaitForNamedCacheSync(c.ctx, infs.name, inf.Informer()) {
				return fmt.Errorf("failed to sync cache for %s informer", infs.name)
			}
		}
	}
	return nil
}

func (c *Operator) Sync(ctx context.Context, namespace, name string) error {
	obj, err := c.bkCli.MonitoringV1beta1().QCloudMonitors(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	start := time.Now()
	if err := c.createOrUpdateDeployment(ctx, obj); err != nil {
		return err
	}
	if err := c.createOrUpdateConfigMap(ctx, obj); err != nil {
		return err
	}
	if err := c.createOrUpdateService(ctx, obj); err != nil {
		return err
	}
	if err := c.createOrUpdateServiceMonitor(ctx, obj); err != nil {
		return err
	}

	key := fmt.Sprintf("%s/%s", namespace, name)
	defaultMetricMonitor.IncReconcileQCloudMonitorSuccessCounter(key)
	defaultMetricMonitor.ObserveReconcileQCloudMonitorDuration(key, time.Since(start))
	logger.Infof("reconcile QCloudmonitor (%s), take: %v", key, time.Since(start))
	return nil
}

func (c *Operator) createOrUpdateConfigMap(ctx context.Context, qcm *bkv1beta1.QCloudMonitor) error {
	selector := map[string]string{
		labelAppManagedBy: define.AppName,
		labelAppInstance:  qcm.Name,
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            qcm.Name,
			Namespace:       qcm.Namespace,
			Labels:          selector,
			OwnerReferences: []metav1.OwnerReference{OwnerRef(qcm)},
		},
		Data: map[string]string{
			"qcloud.yml": qcm.Spec.Config.FileContent,
		},
	}

	cli := c.client.CoreV1().ConfigMaps(qcm.Namespace)
	return k8sutils.CreateOrUpdateConfigMap(ctx, cli, configMap)
}

func (c *Operator) createOrUpdateService(ctx context.Context, qcm *bkv1beta1.QCloudMonitor) error {
	selector := map[string]string{
		labelAppManagedBy: define.AppName,
		labelAppInstance:  qcm.Name,
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            qcm.Name,
			Namespace:       qcm.Namespace,
			Labels:          selector,
			OwnerReferences: []metav1.OwnerReference{OwnerRef(qcm)},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       8080,
				Protocol:   corev1.ProtocolTCP,
				TargetPort: intstr.FromInt32(8080),
			}},
			Selector: selector,
		},
	}

	cli := c.client.CoreV1().Services(qcm.Namespace)
	return k8sutils.CreateOrUpdateService(ctx, cli, service)
}

func (c *Operator) createOrUpdateDeployment(ctx context.Context, qcm *bkv1beta1.QCloudMonitor) error {
	selector := map[string]string{
		labelAppManagedBy: define.AppName,
		labelAppInstance:  qcm.Name,
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            qcm.Name,
			Namespace:       qcm.Namespace,
			Labels:          selector,
			OwnerReferences: []metav1.OwnerReference{OwnerRef(qcm)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: selector,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "qcloud-exporter",
							Args: []string{
								"/usr/bin/qcloud_exporter --config.file=/usr/bin/config/qcloud.yml --web.listen-address=:8080",
							},
							Command: []string{
								"/bin/sh",
								"-c",
								"--",
							},
							Image:           qcm.Spec.Exporter.Image,
							ImagePullPolicy: qcm.Spec.Exporter.ImagePullPolicy,
							Resources:       qcm.Spec.Exporter.Resources,
							VolumeMounts: []corev1.VolumeMount{{
								MountPath: "/usr/bin/config",
								Name:      "config",
								ReadOnly:  true,
							}},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: qcm.Name},
								},
							},
						},
					},
				},
			},
		},
	}

	cli := c.client.AppsV1().Deployments(qcm.Namespace)
	return k8sutils.CreateOrUpdateDeployment(ctx, cli, deployment)
}

func (c *Operator) createOrUpdateServiceMonitor(ctx context.Context, qcm *bkv1beta1.QCloudMonitor) error {
	selector := map[string]string{
		labelAppManagedBy: define.AppName,
		labelAppInstance:  qcm.Name,
	}

	serviceMonitor := &promv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:            qcm.Name,
			Namespace:       qcm.Namespace,
			Labels:          selector,
			OwnerReferences: []metav1.OwnerReference{OwnerRef(qcm)},
		},
		Spec: promv1.ServiceMonitorSpec{
			NamespaceSelector: promv1.NamespaceSelector{
				MatchNames: []string{qcm.Namespace},
			},
			Endpoints: []promv1.Endpoint{{
				Port: "http",
				Path: "/metrics",
			}},
			Selector: metav1.LabelSelector{
				MatchLabels: selector,
			},
		},
	}

	cli := c.promCli.MonitoringV1().ServiceMonitors(qcm.Namespace)
	return k8sutils.CreateOrUpdateServiceMonitor(ctx, cli, serviceMonitor)
}
