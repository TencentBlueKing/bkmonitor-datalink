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

	tkexversiond "github.com/Tencent/bk-bcs/bcs-scenarios/kourse/pkg/client/clientset/versioned"
	promversioned "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"github.com/prometheus-operator/prometheus-operator/pkg/k8sutil"
	"github.com/prometheus-operator/prometheus-operator/pkg/operator"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	clientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"

	bkversioned "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/logconf"
)

func NewK8SClient(host string, tlsConfig *rest.TLSClientConfig) (kubernetes.Interface, error) {
	cfg, err := k8sutil.NewClusterConfig(host, tlsConfig.Insecure, tlsConfig)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func NewK8SClientInsecure() (kubernetes.Interface, error) {
	cfg, err := k8sutil.NewClusterConfig("", true, nil)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// NewPromClient 操作 ServiceMonitor/PodMonitor/Probe CRD
func NewPromClient(host string, tlsConfig *rest.TLSClientConfig) (promversioned.Interface, error) {
	cfg, err := k8sutil.NewClusterConfig(host, tlsConfig.Insecure, tlsConfig)
	if err != nil {
		return nil, err
	}
	return promversioned.NewForConfig(cfg)
}

// NewBKClient 操作 DataID CRD
func NewBKClient(host string, tlsConfig *rest.TLSClientConfig) (bkversioned.Interface, error) {
	cfg, err := k8sutil.NewClusterConfig(host, tlsConfig.Insecure, tlsConfig)
	if err != nil {
		return nil, err
	}
	return bkversioned.NewForConfig(cfg)
}

// NewTkexClient 操作 GameStatefulSet/GameDeployment CRD
func NewTkexClient(host string, tlsConfig *rest.TLSClientConfig) (tkexversiond.Interface, error) {
	cfg, err := k8sutil.NewClusterConfig(host, tlsConfig.Insecure, tlsConfig)
	if err != nil {
		return nil, err
	}
	return tkexversiond.NewForConfig(cfg)
}

func WaitForNamedCacheSync(ctx context.Context, controllerName string, inf cache.SharedIndexInformer) bool {
	return operator.WaitForNamedCacheSync(ctx, controllerName, logconf.New(controllerName), inf)
}

func CreateOrUpdateSecret(ctx context.Context, secretClient clientv1.SecretInterface, desired *corev1.Secret) error {
	return k8sutil.CreateOrUpdateSecret(ctx, secretClient, desired)
}

func mergeMetadata(new *metav1.ObjectMeta, old metav1.ObjectMeta) {
	new.ResourceVersion = old.ResourceVersion

	new.SetLabels(mergeMaps(new.Labels, old.Labels))
	new.SetAnnotations(mergeMaps(new.Annotations, old.Annotations))
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

func CreateOrUpdateConfigMap(ctx context.Context, cmClient clientv1.ConfigMapInterface, desired *corev1.ConfigMap) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existingSecret, err := cmClient.Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}

			_, err = cmClient.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}

		mutated := existingSecret.DeepCopyObject().(*corev1.ConfigMap)
		mergeMetadata(&desired.ObjectMeta, mutated.ObjectMeta)
		if apiequality.Semantic.DeepEqual(existingSecret, desired) {
			return nil
		}
		_, err = cmClient.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	})
}

func CreateOrUpdateService(ctx context.Context, serviceClient clientv1.ServiceInterface, desired *corev1.Service) error {
	return k8sutil.CreateOrUpdateService(ctx, serviceClient, desired)
}

func CreateOrUpdateEndpoints(ctx context.Context, endpointClient clientv1.EndpointsInterface, desired *corev1.Endpoints) error {
	return k8sutil.CreateOrUpdateEndpoints(ctx, endpointClient, desired)
}

func GetSecretDataBySecretKeySelector(ctx context.Context, secretClient clientv1.SecretInterface, selector corev1.SecretKeySelector) (string, error) {
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
