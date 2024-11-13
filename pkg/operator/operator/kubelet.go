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

	logger.Infof("list kubelet servcie %s, count (%d)", cfg, len(obj.Items))
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
		logger.Errorf("syncing nodes into Endpoints object failed, error: %s", err)
	}

	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			if err := c.syncNodeEndpoints(ctx); err != nil {
				logger.Errorf("refresh kubelet endpoints failed, error: %s", err)
			}
		}
	}
}

func (c *Operator) syncNodeEndpoints(ctx context.Context) error {
	c.cleanupDeprecatedService(ctx) // 先清理弃用 service

	cfg := configs.G().Kubelet
	eps := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:   cfg.Name,
			Labels: kubeletServiceLabels.Labels(),
		},
		Subsets: []corev1.EndpointSubset{
			{
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

	nodes, err := c.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "listing nodes failed")
	}
	logger.Debugf("Nodes retrieved from the Kubernetes API, num_nodes:%d", len(nodes.Items))

	addresses, errs := getNodeAddresses(nodes)
	for _, err := range errs {
		logger.Errorf("failed to get node address: %s", err)
	}

	eps.Subsets[0].Addresses = addresses
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

	err = k8sutils.CreateOrUpdateService(ctx, c.client.CoreV1().Services(cfg.Namespace), svc)
	if err != nil {
		return errors.Wrap(err, "synchronizing kubelet service object failed")
	}
	logger.Debugf("sync kubelet service %s", cfg)

	err = k8sutils.CreateOrUpdateEndpoints(ctx, c.client.CoreV1().Endpoints(cfg.Namespace), eps)
	if err != nil {
		return errors.Wrap(err, "synchronizing kubelet endpoints object failed")
	}
	logger.Debugf("sync kubelet endpoints %s, address count (%d)", cfg, len(addresses))

	return nil
}

func getNodeAddresses(nodes *corev1.NodeList) ([]corev1.EndpointAddress, []error) {
	addresses := make([]corev1.EndpointAddress, 0)
	errs := make([]error, 0)

	for _, n := range nodes.Items {
		address, _, err := k8sutils.GetNodeAddress(n)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to determine hostname for node (%s)", n.Name))
			continue
		}
		addresses = append(addresses, corev1.EndpointAddress{
			IP: address,
			TargetRef: &corev1.ObjectReference{
				Kind:       "Node",
				Name:       n.Name,
				UID:        n.UID,
				APIVersion: n.APIVersion,
			},
		})
	}

	return addresses, errs
}
