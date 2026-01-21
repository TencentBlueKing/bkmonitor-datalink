// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package k8sutils

import (
	"context"
	"errors"
	"fmt"
	"strings"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promcli "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	promv1iface "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	promk8sutil "github.com/prometheus-operator/prometheus-operator/pkg/k8sutil"
	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/operator"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	appsv1iface "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1iface "k8s.io/client-go/kubernetes/typed/core/v1"
	discoveryv1iface "k8s.io/client-go/kubernetes/typed/discovery/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"

	bkcli "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/logx"
)

const (
	contentTypeProtobuf = "application/vnd.kubernetes.protobuf"
)

func NewK8SClient(host string, tlsConfig *rest.TLSClientConfig) (kubernetes.Interface, error) {
	cfg, err := promk8sutil.NewClusterConfig(host, tlsConfig.Insecure, tlsConfig)
	if err != nil {
		return nil, err
	}
	cfg.ContentType = contentTypeProtobuf
	return kubernetes.NewForConfig(cfg)
}

func NewMetadataClient(host string, tlsConfig *rest.TLSClientConfig) (metadata.Interface, error) {
	cfg, err := promk8sutil.NewClusterConfig(host, tlsConfig.Insecure, tlsConfig)
	if err != nil {
		return nil, err
	}
	cfg.ContentType = contentTypeProtobuf
	return metadata.NewForConfig(cfg)
}

func NewK8SClientInsecure() (kubernetes.Interface, error) {
	cfg, err := promk8sutil.NewClusterConfig("", true, nil)
	if err != nil {
		return nil, err
	}
	cfg.ContentType = contentTypeProtobuf
	return kubernetes.NewForConfig(cfg)
}

func NewPromClient(host string, tlsConfig *rest.TLSClientConfig) (promcli.Interface, error) {
	cfg, err := promk8sutil.NewClusterConfig(host, tlsConfig.Insecure, tlsConfig)
	if err != nil {
		return nil, err
	}
	return promcli.NewForConfig(cfg)
}

func NewBKClient(host string, tlsConfig *rest.TLSClientConfig) (bkcli.Interface, error) {
	cfg, err := promk8sutil.NewClusterConfig(host, tlsConfig.Insecure, tlsConfig)
	if err != nil {
		return nil, err
	}
	cfg.ContentType = contentTypeProtobuf
	return bkcli.NewForConfig(cfg)
}

func WaitForNamedCacheSync(ctx context.Context, controllerName string, inf cache.SharedIndexInformer) bool {
	return promoperator.WaitForNamedCacheSync(ctx, controllerName, logx.New(controllerName), inf)
}

func mergeMaps(new map[string]string, old map[string]string) map[string]string {
	return mergeMapsByPrefix(new, old, "")
}

func mergeMapsByPrefix(from map[string]string, to map[string]string, prefix string) map[string]string {
	if to == nil {
		to = make(map[string]string)
	}

	if from == nil {
		from = make(map[string]string)
	}

	for k, v := range from {
		if strings.HasPrefix(k, prefix) {
			to[k] = v
		}
	}

	return to
}

func mergeMetadata(new *metav1.ObjectMeta, old metav1.ObjectMeta) {
	new.ResourceVersion = old.ResourceVersion

	new.SetLabels(mergeMaps(new.Labels, old.Labels))
	new.SetAnnotations(mergeMaps(new.Annotations, old.Annotations))
}

func mergeKubectlAnnotations(from *metav1.ObjectMeta, to metav1.ObjectMeta) {
	from.SetAnnotations(mergeMapsByPrefix(from.Annotations, to.Annotations, "kubectl.kubernetes.io/"))
}

func CreateOrUpdateEndpoints(ctx context.Context, cli corev1iface.EndpointsInterface, desired *corev1.Endpoints) error {
	return promk8sutil.CreateOrUpdateEndpoints(ctx, cli, desired)
}

func CreateOrUpdateEndpointSlice(ctx context.Context, cli discoveryv1iface.EndpointSliceInterface, desired *discoveryv1.EndpointSlice) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := cli.Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
			_, err = cli.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		mergeMetadata(&desired.ObjectMeta, existing.ObjectMeta)
		// 使用 DeepEqual 检查是否需要更新，避免不必要的 API 调用
		// 注意：有序的 address 列表可以确保 DeepEqual 比较的准确性
		if apiequality.Semantic.DeepEqual(existing, desired) {
			return nil
		}
		_, err = cli.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	})
}

func CreateOrUpdateSecret(ctx context.Context, cli corev1iface.SecretInterface, desired *corev1.Secret) error {
	return promk8sutil.CreateOrUpdateSecret(ctx, cli, desired)
}

func CreateOrUpdateService(ctx context.Context, cli corev1iface.ServiceInterface, desired *corev1.Service) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		service, err := cli.Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
			_, err = cli.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		// Apply immutable fields from the existing service.
		desired.Spec.IPFamilies = service.Spec.IPFamilies
		desired.Spec.IPFamilyPolicy = service.Spec.IPFamilyPolicy
		desired.Spec.ClusterIP = service.Spec.ClusterIP
		desired.Spec.ClusterIPs = service.Spec.ClusterIPs

		mergeMetadata(&desired.ObjectMeta, service.ObjectMeta)
		_, err = cli.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	})
}

func CreateOrUpdateServiceMonitor(ctx context.Context, cli promv1iface.ServiceMonitorInterface, desired *promv1.ServiceMonitor) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		serviceMonitor, err := cli.Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
			_, err = cli.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}

		mutated := serviceMonitor.DeepCopyObject().(*promv1.ServiceMonitor)
		mergeMetadata(&desired.ObjectMeta, mutated.ObjectMeta)
		if apiequality.Semantic.DeepEqual(serviceMonitor, desired) {
			return nil
		}

		_, err = cli.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	})
}

func CreateOrUpdateConfigMap(ctx context.Context, cli corev1iface.ConfigMapInterface, desired *corev1.ConfigMap) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		configMap, err := cli.Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
			_, err = cli.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}

		mutated := configMap.DeepCopyObject().(*corev1.ConfigMap)
		mergeMetadata(&desired.ObjectMeta, mutated.ObjectMeta)
		if apiequality.Semantic.DeepEqual(configMap, desired) {
			return nil
		}

		_, err = cli.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	})
}

func CreateOrUpdateDeployment(ctx context.Context, cli appsv1iface.DeploymentInterface, desired *appsv1.Deployment) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment, err := cli.Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
			_, err = cli.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}

		mergeMetadata(&desired.ObjectMeta, deployment.ObjectMeta)
		mergeKubectlAnnotations(&deployment.Spec.Template.ObjectMeta, desired.Spec.Template.ObjectMeta)
		_, err = cli.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	})
}

func GetSecretDataBySecretKeySelector(ctx context.Context, secretClient corev1iface.SecretInterface, selector corev1.SecretKeySelector) (string, error) {
	secret, err := secretClient.Get(ctx, selector.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if data, ok := secret.Data[selector.Key]; ok {
		return string(data), nil
	}
	return "", errors.New("secret key not found")
}

// GetNodeAddress returns the provided node's address, based on the priority:
// 1. NodeInternalIP
// 2. NodeExternalIP
//
// Copied from github.com/prometheus/prometheus/discovery/kubernetes/node.go
func GetNodeAddress(node corev1.Node) (string, map[corev1.NodeAddressType][]string, error) {
	m := map[corev1.NodeAddressType][]string{}
	for _, a := range node.Status.Addresses {
		m[a.Type] = append(m[a.Type], a.Address)
	}

	if addresses, ok := m[corev1.NodeInternalIP]; ok {
		return addresses[0], m, nil
	}
	if addresses, ok := m[corev1.NodeExternalIP]; ok {
		return addresses[0], m, nil
	}
	return "", m, fmt.Errorf("host address unknown")
}
