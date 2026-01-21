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
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var kubeletServiceLabels = WrapLabels{
	"k8s-app":                      "kubelet",
	"app.kubernetes.io/name":       "kubelet",
	"app.kubernetes.io/managed-by": "bkmonitor-operator",
}

type WrapLabels map[string]string

func (lbs WrapLabels) Labels() map[string]string {
	dst := make(map[string]string)
	for k, v := range lbs {
		dst[k] = v
	}
	return dst
}

func (lbs WrapLabels) Matcher() string {
	var ret []string
	for k, v := range lbs {
		ret = append(ret, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(ret)
	return strings.Join(ret, ",")
}

func (c *Operator) cleanupDeprecatedService(ctx context.Context) {
	cfg := configs.G().Kubelet
	if !cfg.Validate() {
		logger.Errorf("invalid kubelet config %s", cfg)
		return
	}

	client := c.client.CoreV1().Services(cfg.Namespace)
	obj, err := client.List(ctx, metav1.ListOptions{LabelSelector: kubeletServiceLabels.Matcher()})
	if err != nil {
		logger.Errorf("failed to list services (%s), err: %v", cfg, err)
		return
	}

	logger.Debugf("list kubelet servcie %s, count (%d)", cfg, len(obj.Items))
	for _, svc := range obj.Items {
		if svc.Namespace == cfg.Namespace && svc.Name == cfg.Name {
			continue
		}

		// 清理弃用 service 避免数据重复采集
		err := client.Delete(ctx, svc.Name, metav1.DeleteOptions{})
		if err != nil {
			logger.Errorf("failed to delete service %s/%s, err: %v", svc.Namespace, svc.Name, err)
			continue
		}
		logger.Infof("cleanup deprecated service %s/%s", svc.Namespace, svc.Name)
	}
}

// reconcileNodeEndpoints 周期刷新 kubelet 的 service 和 endpoints
func (c *Operator) reconcileNodeEndpoints(ctx context.Context) {
	c.wg.Add(1)
	defer c.wg.Done()

	if err := c.syncNodeEndpoints(ctx); err != nil {
		logger.Errorf("syncing nodes into Endpoints object failed: %s", err)
	}

	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			if err := c.syncNodeEndpoints(ctx); err != nil {
				logger.Errorf("refresh kubelet endpoints failed: %s", err)
			}
		}
	}
}

func (c *Operator) syncNodeEndpoints(ctx context.Context) error {
	c.cleanupDeprecatedService(ctx) // 先清理弃用 service

	cfg := configs.G().Kubelet
	nodes := c.objectsController.NodeObjs()
	logger.Debugf("nodes retrieved from the Kubernetes API, num_nodes: %d", len(nodes))

	addresses, errs := getNodeAddresses(nodes)
	for _, err := range errs {
		logger.Errorf("failed to get node address: %s", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   cfg.Name,
			Labels: kubeletServiceLabels.Labels(),
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
			Ports: []corev1.ServicePort{
				{
					Name: "https-metrics",
					Port: 10250,
				},
				{
					Name: "http-metrics",
					Port: 10255,
				},
				{
					Name: "cadvisor",
					Port: 4194,
				},
			},
		},
	}

	err := k8sutils.CreateOrUpdateService(ctx, c.client.CoreV1().Services(cfg.Namespace), svc)
	if err != nil {
		return errors.Wrap(err, "synchronizing kubelet service object failed")
	}
	logger.Debugf("sync kubelet service %s", cfg)

	// 判断是否使用 EndpointSlice
	if useEndpointslice {
		return c.syncEndpointSlices(ctx, cfg, addresses)
	}

	// 保持原有逻辑：创建 Endpoints（向后兼容）
	eps := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:   cfg.Name,
			Labels: kubeletServiceLabels.Labels(),
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: addresses,
				Ports: []corev1.EndpointPort{
					{
						Name: "https-metrics",
						Port: 10250,
					},
					{
						Name: "http-metrics",
						Port: 10255,
					},
					{
						Name: "cadvisor",
						Port: 4194,
					},
				},
			},
		},
	}

	err = k8sutils.CreateOrUpdateEndpoints(ctx, c.client.CoreV1().Endpoints(cfg.Namespace), eps)
	if err != nil {
		return errors.Wrap(err, "synchronizing kubelet endpoints object failed")
	}
	logger.Debugf("sync kubelet endpoints %s, address count (%d)", cfg, len(addresses))

	return nil
}

// syncEndpointSlices 同步 kubelet 的 EndpointSlice 资源
// 支持将大量节点拆分成多个 EndpointSlice（每个最多 1000 个 endpoints）
func (c *Operator) syncEndpointSlices(ctx context.Context, cfg configs.Kubelet, addresses []corev1.EndpointAddress) error {
	// 获取 Service 的 UID（用于 OwnerReferences）
	svc, err := c.client.CoreV1().Services(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to get service for UID")
	}

	const maxEndpointsPerSlice = 1000
	endpointSliceClient := c.client.DiscoveryV1().EndpointSlices(cfg.Namespace)

	// 计算需要创建的 EndpointSlice 数量
	numSlices := (len(addresses) + maxEndpointsPerSlice - 1) / maxEndpointsPerSlice

	// 获取现有的 EndpointSlice
	existingSlices, err := endpointSliceClient.List(ctx, metav1.ListOptions{
		LabelSelector: kubeletServiceLabels.Matcher(),
	})
	if err != nil {
		return errors.Wrap(err, "failed to list existing endpoint slices")
	}

	// 创建或更新 EndpointSlice
	for i := 0; i < numSlices; i++ {
		start := i * maxEndpointsPerSlice
		end := start + maxEndpointsPerSlice
		if end > len(addresses) {
			end = len(addresses)
		}

		sliceName := fmt.Sprintf("%s-%d", cfg.Name, i)
		endpoints := make([]discoveryv1.Endpoint, 0, end-start)
		for j := start; j < end; j++ {
			endpoints = append(endpoints, discoveryv1.Endpoint{
				Addresses: []string{addresses[j].IP},
				TargetRef: &corev1.ObjectReference{
					Kind:       addresses[j].TargetRef.Kind,
					Name:       addresses[j].TargetRef.Name,
					UID:        addresses[j].TargetRef.UID,
					APIVersion: addresses[j].TargetRef.APIVersion,
				},
			})
		}

		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sliceName,
				Namespace: cfg.Namespace,
				Labels:    kubeletServiceLabels.Labels(),
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "Service",
						Name:       cfg.Name,
						UID:        svc.UID,
					},
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints:   endpoints,
			Ports: []discoveryv1.EndpointPort{
				{Name: stringPtr("https-metrics"), Port: int32Ptr(10250)},
				{Name: stringPtr("http-metrics"), Port: int32Ptr(10255)},
				{Name: stringPtr("cadvisor"), Port: int32Ptr(4194)},
			},
		}

		err := k8sutils.CreateOrUpdateEndpointSlice(ctx, endpointSliceClient, slice)
		if err != nil {
			return errors.Wrapf(err, "failed to create/update endpoint slice %s", sliceName)
		}
	}

	// 删除多余的 EndpointSlice（如果节点数量减少）
	existingSliceMap := make(map[string]bool)
	for _, slice := range existingSlices.Items {
		existingSliceMap[slice.Name] = true
	}
	for i := 0; i < numSlices; i++ {
		sliceName := fmt.Sprintf("%s-%d", cfg.Name, i)
		delete(existingSliceMap, sliceName)
	}
	// 删除不再需要的 EndpointSlice
	for sliceName := range existingSliceMap {
		err := endpointSliceClient.Delete(ctx, sliceName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			logger.Errorf("failed to delete endpoint slice %s: %v", sliceName, err)
		}
	}

	// 删除原来的 Endpoints 资源，避免 EndpointSlice Mirroring Controller 继续镜像
	// 这可以防止创建重复的 EndpointSlice 或产生冲突
	endpointsClient := c.client.CoreV1().Endpoints(cfg.Namespace)
	err = endpointsClient.Delete(ctx, cfg.Name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Warnf("failed to delete endpoints %s: %v (this is not critical, but may cause duplicate endpoint slices)", cfg.Name, err)
	} else if err == nil {
		logger.Debugf("deleted old endpoints %s to avoid mirroring conflicts", cfg.Name)
	}

	logger.Debugf("sync kubelet endpoint slices %s, address count (%d), slice count (%d)", cfg, len(addresses), numSlices)

	return nil
}

// stringPtr returns a pointer to the given string
func stringPtr(s string) *string {
	return &s
}

// int32Ptr returns a pointer to the given int32
func int32Ptr(i int32) *int32 {
	return &i
}

func getNodeAddresses(nodes []*corev1.Node) ([]corev1.EndpointAddress, []error) {
	addresses := make([]corev1.EndpointAddress, 0)
	errs := make([]error, 0)

	for i := 0; i < len(nodes); i++ {
		node := nodes[i]
		address, _, err := k8sutils.GetNodeAddress(*node)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to determine hostname for node (%s)", node.Name))
			continue
		}
		addresses = append(addresses, corev1.EndpointAddress{
			IP: address,
			TargetRef: &corev1.ObjectReference{
				Kind:       "Node",
				Name:       node.Name,
				UID:        node.UID,
				APIVersion: node.APIVersion,
			},
		})
	}

	return addresses, errs
}
