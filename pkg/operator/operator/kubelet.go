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
	"sync"
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

var (
	// deleteEndpointsOnce 确保删除 Endpoints 的操作只执行一次（进程内）
	// 使用 sync.Once 可以避免每次同步都检查 Endpoints，提升性能
	deleteEndpointsOnce sync.Once
)

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

// reconcileNodeEndpoints 周期刷新 kubelet 的 service 和 endpoints/endpointslice
// 该函数在后台 goroutine 中运行，每 3 分钟同步一次
// 确保 kubelet Service 和 Endpoints/EndpointSlice 资源始终与集群中的节点保持一致
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

// syncNodeEndpoints 同步 kubelet 的 Service 和 Endpoints/EndpointSlice 资源
// 该函数负责：
// 1. 清理弃用的 Service 资源，避免数据重复采集
// 2. 创建或更新 kubelet Service（Headless Service，ClusterIP=None）
// 3. 根据 useEndpointslice 标志选择创建 EndpointSlice 或 Endpoints：
//   - 如果启用 EndpointSlice（K8s >= 1.21.0 且配置启用），则创建 EndpointSlice 并支持拆分
//   - 否则创建传统的 Endpoints 资源（向后兼容）
//
// 背景说明：
//   - kubelet Service 是一个无 selector 的 Headless Service，用于暴露所有节点的 kubelet 指标端口
//   - 当集群节点数超过 1000 时，传统的 Endpoints 资源会被 EndpointSlice Mirroring Controller
//     镜像为 EndpointSlice，但受限于 --mirroring-max-endpoints-per-subset=1000 配置，只会镜像
//     前 1000 个地址，导致部分节点无法被采集
//   - 直接创建 EndpointSlice 可以绕过该限制，通过手动拆分多个 EndpointSlice 来支持超大规模集群
func (c *Operator) syncNodeEndpoints(ctx context.Context) error {
	// 先清理弃用 service，避免数据重复采集
	c.cleanupDeprecatedService(ctx)

	cfg := configs.G().Kubelet
	nodes := c.objectsController.NodeObjs()
	logger.Debugf("nodes retrieved from the Kubernetes API, num_nodes: %d", len(nodes))

	// 从所有节点提取 IP 地址和节点引用信息
	addresses, errs := getNodeAddresses(nodes)
	for _, err := range errs {
		logger.Errorf("failed to get node address: %s", err)
	}

	// 创建或更新 kubelet Service（Headless Service）
	// ClusterIP=None 表示这是一个 Headless Service，不会分配 ClusterIP
	// 该 Service 用于服务发现，通过 Endpoints/EndpointSlice 暴露所有节点的 kubelet 端口
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   cfg.Name,
			Labels: kubeletServiceLabels.Labels(),
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None", // Headless Service
			Ports: []corev1.ServicePort{
				{
					Name: "https-metrics", // kubelet HTTPS 指标端口
					Port: 10250,
				},
				{
					Name: "http-metrics", // kubelet HTTP 指标端口（已弃用，但保留兼容性）
					Port: 10255,
				},
				{
					Name: "cadvisor", // cAdvisor 容器指标端口
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
	// useEndpointslice 在 operator.go 中初始化，当 K8s >= 1.21.0 且配置启用时为 true
	// 使用 EndpointSlice 可以支持超过 1000 个节点的集群，通过拆分多个 EndpointSlice 实现
	if useEndpointslice {
		return c.syncEndpointSlices(ctx, cfg, addresses)
	}

	// 保持原有逻辑：创建 Endpoints（向后兼容）
	// 适用于 K8s < 1.21.0 或未启用 EndpointSlice 的场景
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

	// 创建或更新 Endpoints 资源（传统方式，向后兼容）
	err = k8sutils.CreateOrUpdateEndpoints(ctx, c.client.CoreV1().Endpoints(cfg.Namespace), eps)
	if err != nil {
		return errors.Wrap(err, "synchronizing kubelet endpoints object failed")
	}
	logger.Debugf("sync kubelet endpoints %s, address count (%d)", cfg, len(addresses))

	return nil
}

// syncEndpointSlices 同步 kubelet 的 EndpointSlice 资源
//
// 功能说明：
// 1. 将大量节点拆分成多个 EndpointSlice，每个 EndpointSlice 最多包含的 endpoints 数量可配置（默认 1000）
// 2. 支持超大规模集群（节点数 > 1000），解决 EndpointSlice Mirroring Controller 的限制问题
// 3. 自动清理多余的 EndpointSlice（当节点数量减少时）
// 4. 删除旧的 Endpoints 资源，避免与 EndpointSlice Mirroring Controller 产生冲突
//
// 为什么需要拆分？
//   - Kubernetes 的 EndpointSlice Mirroring Controller 在镜像 Endpoints 到 EndpointSlice 时，
//     受限于 --mirroring-max-endpoints-per-subset=1000 配置，只会镜像前 1000 个地址
//   - 对于超过 1000 个节点的集群，直接创建 EndpointSlice 可以绕过该限制
//   - 每个 EndpointSlice 最多包含的 endpoints 数量由配置项 max_endpoints_per_slice 控制（默认 1000）
//
// 配置说明：
// - max_endpoints_per_slice：每个 EndpointSlice 最多包含的 endpoints 数量
//   - 默认值：100（Kubernetes 的默认值）
//   - 最大值：1000（Kubernetes 的硬限制）
//   - 可以通过配置文件中的 kubelet.max_endpoints_per_slice 进行配置
//   - 如果配置未设置或无效（<= 0），则使用默认值 100
//
// - 超过该限制会导致 EndpointSlice 创建失败
// - 参考：https://kubernetes.io/docs/reference/command-line-tools/kube-controller-manager/
func (c *Operator) syncEndpointSlices(ctx context.Context, cfg configs.Kubelet, addresses []corev1.EndpointAddress) error {
	endpointSliceClient := c.client.DiscoveryV1().EndpointSlices(cfg.Namespace)
	endpointsClient := c.client.CoreV1().Endpoints(cfg.Namespace)

	// ========== 前置检查：确保 Service 存在 ==========
	// Service 必须在删除操作之前检查，如果不存在则直接报错返回
	// 这样可以避免在 Service 不存在的情况下删除 Endpoints 或 EndpointSlice，导致数据上报完全中断
	// 原则：少了总比没有好，如果 Service 不存在，保留现有的 Endpoints/EndpointSlice 至少还能部分采集
	svc, err := c.client.CoreV1().Services(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to get service, aborting sync to avoid data loss")
	}

	// ========== 第一步：删除操作（放在开头，避免重复采集） ==========

	// 1. 删除原来的 Endpoints 资源（仅在第一次启动时执行，避免 EndpointSlice Mirroring Controller 继续镜像）
	// 使用 sync.Once 确保删除操作只执行一次（进程内），提升性能
	// 判断逻辑：检查 Endpoints 是否存在，如果存在则删除（说明是从旧版本升级或首次使用 EndpointSlice）
	// 背景说明：
	// - 当我们直接创建 EndpointSlice 时，如果 Endpoints 资源仍然存在，
	//   EndpointSlice Mirroring Controller 会尝试将 Endpoints 镜像为 EndpointSlice
	// - 这会导致创建重复的 EndpointSlice 或产生冲突（例如命名冲突）
	// - 使用 sync.Once + 检查 Endpoints 是否存在，既保证了只执行一次，又保证了逻辑正确性
	deleteEndpointsOnce.Do(func() {
		_, err := endpointsClient.Get(ctx, cfg.Name, metav1.GetOptions{})
		if err == nil {
			// Endpoints 资源存在，说明可能是从旧版本升级或首次使用 EndpointSlice
			// 删除它以避免 EndpointSlice Mirroring Controller 继续镜像
			err = endpointsClient.Delete(ctx, cfg.Name, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				logger.Warnf("failed to delete endpoints %s: %v (this is not critical, but may cause duplicate endpoint slices)", cfg.Name, err)
			} else if err == nil {
				logger.Debugf("deleted old endpoints %s to avoid mirroring conflicts (first startup)", cfg.Name)
			}
		} else if !apierrors.IsNotFound(err) {
			// 获取失败且不是 NotFound 错误，记录警告但不中断流程
			logger.Warnf("failed to check endpoints %s: %v", cfg.Name, err)
		}
		// 如果 Endpoints 不存在（NotFound），说明已经被删除或从未创建，无需处理
	})

	// 2. 计算需要创建的 EndpointSlice 数量（用于确定哪些 slice 需要保留）
	// 从配置中获取每个 EndpointSlice 最多包含的 endpoints 数量
	// 注意：边界检查已在 setupKubelet 函数中完成，这里直接使用配置值
	maxEndpointsPerSlice := cfg.MaxEndpointsPerSlice
	// 计算需要创建的 EndpointSlice 数量（向上取整）
	// 公式说明：(a + b - 1) / b 是整数向上取整的经典技巧
	// - 当 a 能被 b 整除时：(a + b - 1) / b = a / b（例如：1000 / 1000 = 1）
	// - 当 a 不能被 b 整除时：(a + b - 1) / b 会多出 1（例如：1181 / 1000 = 1.181，向上取整为 2）
	// 示例：1181 个节点，maxEndpointsPerSlice=1000 时，需要 2 个 EndpointSlice（1000 + 181）
	numSlices := (len(addresses) + maxEndpointsPerSlice - 1) / maxEndpointsPerSlice

	// 3. 清理多余的 EndpointSlice（当节点数量减少时）
	// 例如：如果之前有 1181 个节点（需要 2 个 slice），现在只有 800 个节点（只需要 1 个 slice），
	// 则需要删除多余的 slice（bkmonitor-operator-stack-kubelet-1）
	existingSlices, err := endpointSliceClient.List(ctx, metav1.ListOptions{
		LabelSelector: kubeletServiceLabels.Matcher(),
	})
	if err != nil {
		return errors.Wrap(err, "failed to list existing endpoint slices")
	}

	// 构建需要保留的 slice 名称集合
	// 使用 map[string]struct{} 而不是 map[string]bool，性能更好且语义更清晰
	neededSliceMap := make(map[string]struct{})
	for i := 0; i < numSlices; i++ {
		sliceName := fmt.Sprintf("%s-%d", cfg.Name, i)
		neededSliceMap[sliceName] = struct{}{}
	}

	// 删除不再需要的 EndpointSlice（避免重复采集）
	for _, slice := range existingSlices.Items {
		if _, exists := neededSliceMap[slice.Name]; !exists {
			err := endpointSliceClient.Delete(ctx, slice.Name, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				// 如果删除失败且不是 NotFound 错误，记录错误但不中断流程
				// NotFound 错误可以忽略（可能已经被其他进程删除）
				logger.Errorf("failed to delete endpoint slice %s: %v", slice.Name, err)
			} else if err == nil {
				logger.Debugf("deleted unnecessary endpoint slice %s", slice.Name)
			}
		}
	}

	// ========== 第二步：创建或更新 EndpointSlice ==========
	// 注意：Service 已在函数开头检查，这里直接使用 svc 变量

	// 创建或更新每个 EndpointSlice
	// 将 addresses 数组按 maxEndpointsPerSlice 大小切分成多个批次
	for i := 0; i < numSlices; i++ {
		// 计算当前 EndpointSlice 的地址范围
		start := i * maxEndpointsPerSlice
		end := start + maxEndpointsPerSlice
		if end > len(addresses) {
			end = len(addresses) // 最后一个 slice 可能不足 1000 个
		}

		// EndpointSlice 命名规则：{service-name}-{index}
		// 例如：bkmonitor-operator-stack-kubelet-0, bkmonitor-operator-stack-kubelet-1
		sliceName := fmt.Sprintf("%s-%d", cfg.Name, i)

		// 构建当前 slice 的 endpoints 列表
		endpoints := make([]discoveryv1.Endpoint, 0, end-start)
		for j := start; j < end; j++ {
			endpoints = append(endpoints, discoveryv1.Endpoint{
				Addresses: []string{addresses[j].IP}, // 节点 IP 地址
				TargetRef: &corev1.ObjectReference{ // 节点引用信息，用于关联到具体的 Node 资源
					Kind:       addresses[j].TargetRef.Kind,
					Name:       addresses[j].TargetRef.Name,
					UID:        addresses[j].TargetRef.UID,
					APIVersion: addresses[j].TargetRef.APIVersion,
				},
			})
		}

		// 构建 EndpointSlice 对象
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sliceName,
				Namespace: cfg.Namespace,
				Labels:    kubeletServiceLabels.Labels(), // 使用相同的标签，便于查询和管理
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "Service",
						Name:       cfg.Name,
						UID:        svc.UID, // 设置 Service 为 Owner，实现级联删除
					},
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4, // 仅支持 IPv4
			Endpoints:   endpoints,
			Ports: []discoveryv1.EndpointPort{
				{Name: stringPtr("https-metrics"), Port: int32Ptr(10250)}, // kubelet HTTPS 指标端口
				{Name: stringPtr("http-metrics"), Port: int32Ptr(10255)},  // kubelet HTTP 指标端口
				{Name: stringPtr("cadvisor"), Port: int32Ptr(4194)},       // cAdvisor 容器指标端口
			},
		}

		// 创建或更新 EndpointSlice（如果已存在则更新，不存在则创建）
		err := k8sutils.CreateOrUpdateEndpointSlice(ctx, endpointSliceClient, slice)
		if err != nil {
			return errors.Wrapf(err, "failed to create/update endpoint slice %s", sliceName)
		}
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

// getNodeAddresses 从节点列表中提取 IP 地址和节点引用信息
// 返回：
//   - addresses: 成功提取的节点地址列表，用于创建 Endpoints/EndpointSlice（按节点名称排序）
//   - errs: 提取失败的节点错误列表（单个节点失败不影响其他节点）
//
// 注意：
//   - 即使部分节点地址提取失败，函数仍会返回成功提取的地址列表
//   - 节点列表会按节点名称排序，确保 EndpointSlice 内容稳定
//   - 调用方需要根据 errs 决定是否记录警告或错误日志
func getNodeAddresses(nodes []*corev1.Node) ([]corev1.EndpointAddress, []error) {
	// 对节点列表按名称排序，确保 EndpointSlice 内容稳定
	// 因为 NodeMap.GetAll() 返回的节点列表来自 map，顺序是随机的
	sortedNodes := make([]*corev1.Node, len(nodes))
	copy(sortedNodes, nodes)
	sort.Slice(sortedNodes, func(i, j int) bool {
		return sortedNodes[i].Name < sortedNodes[j].Name
	})

	addresses := make([]corev1.EndpointAddress, 0)
	errs := make([]error, 0)

	for i := 0; i < len(sortedNodes); i++ {
		node := sortedNodes[i]
		// 获取节点的 IP 地址（优先使用 InternalIP，其次 ExternalIP）
		address, _, err := k8sutils.GetNodeAddress(*node)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to determine hostname for node (%s)", node.Name))
			continue
		}
		// 构建 EndpointAddress，包含节点 IP 和节点引用信息
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
