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
	discoveryv1iface "k8s.io/client-go/kubernetes/typed/discovery/v1"

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

	// commonEndpointPorts 定义 kubelet EndpointSlice 的公共端口配置
	// 注意：这是一个只读的常量，所有 EndpointSlice 共享这个配置
	// 由于 Ports 字段是只读的（不会修改），所以可以安全地共享，避免重复创建
	commonEndpointPorts = []discoveryv1.EndpointPort{
		{Name: stringPtr("https-metrics"), Port: int32Ptr(10250)}, // kubelet HTTPS 指标端口
		{Name: stringPtr("http-metrics"), Port: int32Ptr(10255)},  // kubelet HTTP 指标端口（已弃用，但保留兼容性）
		{Name: stringPtr("cadvisor"), Port: int32Ptr(4194)},       // cAdvisor 容器指标端口
	}
)

// endpointSliceAnalysisResult 分析结果，包含需要删除、同步的 slices 信息
type endpointSliceAnalysisResult struct {
	// SlicesToDelete 需要删除的 slice 名称集合
	SlicesToDelete map[string]struct{}
	// SlicesToSync 需要同步的 slices（包括需要更新和新建的 slices）
	// 注意：底层函数 CreateOrUpdateEndpointSlice 已经支持创建或更新，所以可以统一处理
	SlicesToSync []*discoveryv1.EndpointSlice
	// TotalSlices 调整后的总 slice 数量
	TotalSlices int
	// Service Service 对象，如果为 nil 表示 Service 不存在，需要删除所有 EndpointSlice
	Service *corev1.Service
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
		logger.Infof("[kubelet-endpointslice] using EndpointSlice mode, nodes=%d, max_endpoints_per_slice=%d", len(addresses), cfg.MaxEndpointsPerSlice)
		return c.syncEndpointSlices(ctx, cfg, addresses)
	}
	logger.Debugf("[kubelet-endpointslice] using legacy Endpoints mode (EndpointSlice disabled)")

	// 清理遗留的 EndpointSlice 资源（当从 EndpointSlice 模式切换到 Endpoints 模式时）
	// 背景说明：
	// - 当配置从 useEndpointslice=true 切换到 useEndpointslice=false 时，之前创建的 EndpointSlice 资源会遗留
	// - 需要在切换到 Endpoints 模式时清理这些遗留资源
	endpointSliceClient := c.client.DiscoveryV1().EndpointSlices(cfg.Namespace)
	labelSelector := kubeletServiceLabels.Matcher() + ",!endpointslice.kubernetes.io/managed-by"
	existingSlices, err := endpointSliceClient.List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		logger.Warnf("[kubelet-endpoint] failed to list endpoint slices for cleanup: %v (this is not critical)", err)
	} else if len(existingSlices.Items) > 0 {
		logger.Infof("[kubelet-endpoint] found %d legacy endpoint slices, cleaning up...", len(existingSlices.Items))
		for _, slice := range existingSlices.Items {
			err := endpointSliceClient.Delete(ctx, slice.Name, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				logger.Warnf("[kubelet-endpoint] failed to delete legacy slice %s: %v", slice.Name, err)
			} else if err == nil {
				logger.Debugf("[kubelet-endpoint] deleted legacy endpoint slice: %s", slice.Name)
			}
		}
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
// 1. 将大量节点拆分成多个 EndpointSlice，每个 EndpointSlice 最多包含的 endpoints 数量可配置（默认 100）
// 2. 支持超大规模集群（节点数 > 1000），解决 EndpointSlice Mirroring Controller 的限制问题
// 3. 自动清理多余的 EndpointSlice（当节点数量减少时）
// 4. 删除旧的 Endpoints 资源，避免与 EndpointSlice Mirroring Controller 产生冲突
//
// 为什么需要拆分？
//   - Kubernetes 的 EndpointSlice Mirroring Controller 在镜像 Endpoints 到 EndpointSlice 时，
//     受限于 --mirroring-max-endpoints-per-subset=1000 配置，只会镜像前 1000 个地址
//   - 对于超过 1000 个节点的集群，直接创建 EndpointSlice 可以绕过该限制
//   - 每个 EndpointSlice 最多包含的 endpoints 数量由配置项 max_endpoints_per_slice 控制（默认 100）
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
		endpointsClient := c.client.CoreV1().Endpoints(cfg.Namespace)
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

	// 2. 获取现有的 EndpointSlice（用于优化填充策略）
	// 参考 Kubernetes 官方实现：优先填充已有的 slice，最大化利用空间
	endpointSliceClient := c.client.DiscoveryV1().EndpointSlices(cfg.Namespace)

	// 构建 label selector：过滤掉由 Kubernetes 系统控制器创建的镜像 EndpointSlice
	// 背景说明：
	// - 当我们删除 Endpoint 后，Kubernetes 的 endpointslicemirroring-controller 会自动创建镜像 EndpointSlice
	// - 这些镜像 EndpointSlice 的删除有延迟，可能会被 label selector 匹配到
	// - 它们有标准的 Kubernetes label: endpointslice.kubernetes.io/managed-by=endpointslicemirroring-controller.k8s.io
	// - 通过在 LabelSelector 中添加 !endpointslice.kubernetes.io/managed-by 条件（表示该标签不存在），让 API Server 直接过滤
	labelSelector := kubeletServiceLabels.Matcher() + ",!endpointslice.kubernetes.io/managed-by"

	logger.Debugf("[kubelet-endpointslice] listing existing endpoint slices in namespace=%s, labelSelector=%s", cfg.Namespace, labelSelector)
	existingSlices, err := endpointSliceClient.List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		logger.Errorf("[kubelet-endpointslice] failed to list existing endpoint slices: %v", err)
		return errors.Wrap(err, "failed to list existing endpoint slices")
	}
	logger.Infof("[kubelet-endpointslice] found %d endpoint slices", len(existingSlices.Items))

	// 从配置中获取每个 EndpointSlice 最多包含的 endpoints 数量
	// 注意：边界检查已在 setupKubelet 函数中完成，这里直接使用配置值
	maxEndpointsPerSlice := cfg.MaxEndpointsPerSlice
	rebalanceThreshold := cfg.RebalanceThreshold

	// ========== 分析阶段：计算需要删除、同步的 slices ==========
	// 分析函数内部会：
	// 1. 检查 Service 是否存在（如果不存在，Service 字段为 nil，SlicesToDelete 包含所有现有 slices）
	// 2. 判断是否需要 rebalance（如果需要，构建重新分配的 slices）
	// 3. 正常场景下，计算需要删除、同步的 slices（SlicesToSync 包括需要更新和新建的 slices）
	logger.Debugf("[kubelet-endpointslice] analyzing endpoint slices: addresses=%d, maxEndpointsPerSlice=%d, rebalanceThreshold=%.2f", len(addresses), maxEndpointsPerSlice, rebalanceThreshold)
	analysisResult, err := c.analyzeEndpointSlices(ctx, cfg, existingSlices.Items, addresses, maxEndpointsPerSlice, rebalanceThreshold)
	if err != nil {
		logger.Errorf("[kubelet-endpointslice] failed to analyze endpoint slices: %v", err)
		return errors.Wrap(err, "failed to analyze endpoint slices")
	}
	logger.Infof("[kubelet-endpointslice] analysis result: slices_to_sync=%d, slices_to_delete=%d, total_slices=%d", len(analysisResult.SlicesToSync), len(analysisResult.SlicesToDelete), analysisResult.TotalSlices)

	// ========== 执行阶段：根据分析结果执行删除、同步操作 ==========
	// 执行函数会根据分析结果：
	// 1. 删除不再需要的 slices（所有场景）
	// 2. 如果 Service 不存在，只删除，不更新或创建
	// 3. 如果需要 rebalance，删除所有旧 slices 后同步新的 slices
	// 4. 正常场景下，同步需要同步的 slices（包括更新和新建，统一使用 CreateOrUpdateEndpointSlice）
	logger.Debugf("[kubelet-endpointslice] executing endpoint slice changes")
	err = c.executeEndpointSliceChanges(ctx, endpointSliceClient, cfg, analysisResult)
	if err != nil {
		logger.Errorf("[kubelet-endpointslice] failed to execute endpoint slice changes: %v", err)
		return errors.Wrap(err, "failed to execute endpoint slice changes")
	}

	// Service 不存在时的特殊处理：记录日志并返回
	// 注意：Service 检查已在 analyzeEndpointSlices 函数内部完成，这里只是记录日志
	if analysisResult.Service == nil {
		logger.Infof("[kubelet-endpointslice] cleaned up %d endpoint slices (service %s/%s not found)", len(analysisResult.SlicesToDelete), cfg.Namespace, cfg.Name)
		return errors.New("service not found, endpoint slices cleaned up")
	}

	logger.Infof("[kubelet-endpointslice] sync completed successfully: service=%s/%s, addresses=%d, existing_slices=%d, synced_slices=%d, total_slices=%d", cfg.Namespace, cfg.Name, len(addresses), len(existingSlices.Items), len(analysisResult.SlicesToSync), analysisResult.TotalSlices)

	return nil
}

// convertAddressesToEndpoints 批量将 corev1.EndpointAddress 转换为 discoveryv1.Endpoint
// 这是一个辅助函数，用于消除代码重复
func convertAddressesToEndpoints(addresses []corev1.EndpointAddress) []discoveryv1.Endpoint {
	endpoints := make([]discoveryv1.Endpoint, 0, len(addresses))
	for _, addr := range addresses {
		endpoints = append(endpoints, discoveryv1.Endpoint{
			Addresses: []string{addr.IP},
			TargetRef: &corev1.ObjectReference{
				Kind:       addr.TargetRef.Kind,
				Name:       addr.TargetRef.Name,
				UID:        addr.TargetRef.UID,
				APIVersion: addr.TargetRef.APIVersion,
			},
		})
	}
	return endpoints
}

// analyzeEndpointSlices 分析 EndpointSlice 的变化，计算需要删除、同步的 slices
//
// 功能说明：
// 1. 检查 Service 是否存在，如果不存在则标记需要删除所有 EndpointSlice（Service 字段为 nil）
// 2. 判断是否需要 rebalance（合并 slice），如果需要则构建 rebalance 的 slices
// 3. 正常场景下：
//   - 删除不再需要的 endpoints（当节点减少时）
//   - 填充新的 addresses（优化填充策略，优先填充已有的 slice）
//   - 计算需要删除、更新、新建的 slices
//
// 参数：
//   - ctx: 上下文（用于获取 Service）
//   - cfg: Kubelet 配置
//   - existingSlices: 现有的 EndpointSlice 列表
//   - addresses: 期望的节点地址列表
//   - maxEndpointsPerSlice: 每个 slice 最多包含的 endpoints 数量
//   - rebalanceThreshold: Rebalance 阈值（0.0-1.0）
//
// 返回：
//   - *endpointSliceAnalysisResult: 分析结果，包含需要删除、同步的 slices 信息
//   - 如果 Service 不存在，Service 字段为 nil，SlicesToDelete 包含所有现有 slices
//   - 如果需要 rebalance，SlicesToDelete 包含所有现有 slices，SlicesToSync 包含重新分配的 slices
//   - 正常场景下，包含需要删除、同步的 slices 信息（SlicesToSync 包括需要更新和新建的 slices）
//   - error: 如果分析失败返回错误（如 Service 获取失败）
func (c *Operator) analyzeEndpointSlices(ctx context.Context, cfg configs.Kubelet, existingSlices []discoveryv1.EndpointSlice, addresses []corev1.EndpointAddress, maxEndpointsPerSlice int, rebalanceThreshold float64) (*endpointSliceAnalysisResult, error) {
	result := &endpointSliceAnalysisResult{
		SlicesToDelete: make(map[string]struct{}),
		SlicesToSync:   make([]*discoveryv1.EndpointSlice, 0),
	}

	// ========== 第一步：检查 Service 是否存在 ==========
	logger.Debugf("[kubelet-endpointslice] checking if service %s/%s exists", cfg.Namespace, cfg.Name)
	svc, err := c.client.CoreV1().Services(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Service 不存在，需要在分析函数中处理删除所有 EndpointSlice 的逻辑
			logger.Warnf("[kubelet-endpointslice] service %s/%s not found, will delete all %d related endpoint slices", cfg.Namespace, cfg.Name, len(existingSlices))
			// 收集所有需要删除的 slice 名称
			for _, slice := range existingSlices {
				result.SlicesToDelete[slice.Name] = struct{}{}
			}
			return result, nil
		} else {
			// 其他错误（如网络错误、权限错误等），直接返回
			logger.Errorf("[kubelet-endpointslice] failed to get service %s/%s: %v", cfg.Namespace, cfg.Name, err)
			return nil, errors.Wrap(err, "failed to get service")
		}
	}
	logger.Debugf("[kubelet-endpointslice] service %s/%s exists (UID=%s)", cfg.Namespace, cfg.Name, svc.UID)
	result.Service = svc

	// ========== 第二步：Rebalance 提前判断 ==========
	// Rebalance 的目的：当现有的多个 slice 利用率过低时，合并它们以减少 slice 数量
	// 判断条件：
	// 1. 现有 slice 数量 > 需要的 slice 数量（说明有多余的 slice 可以合并）
	// 2. 现有 slice 的实际利用率 < 阈值（说明空间浪费严重）

	// 计算需要的 slice 数量（向上取整）
	numSlicesNeeded := (len(addresses) + maxEndpointsPerSlice - 1) / maxEndpointsPerSlice

	// 计算现有 slice 的实际使用情况（基于现有 slice 的总容量）
	// 现有总容量 = 现有 slice 数量 × maxEndpointsPerSlice
	// 实际使用 = 当前节点数量（addresses 的长度）
	existingSliceCount := len(existingSlices)
	existingTotalCapacity := existingSliceCount * maxEndpointsPerSlice
	actualUsed := len(addresses)

	// Rebalance 条件：
	// 1. 现有 slice 数量 > 需要的 slice 数量（有多余的 slice）
	// 2. 现有容量 > 0（避免除零错误）
	// 3. 现有 slice 的实际利用率 < 阈值
	shouldRebalance := existingSliceCount > numSlicesNeeded &&
		existingTotalCapacity > 0 &&
		float64(actualUsed)/float64(existingTotalCapacity) < rebalanceThreshold

	if shouldRebalance {
		logger.Infof("[kubelet-endpointslice] rebalance triggered: existing slices=%d > needed=%d, capacity usage %.2f%% (%d/%d) < threshold %.2f%%, merging slices",
			existingSliceCount, numSlicesNeeded, float64(actualUsed)/float64(existingTotalCapacity)*100, actualUsed, existingTotalCapacity, rebalanceThreshold*100)

		// ========== Rebalance 分支：数据准备 ==========
		// 1. 将 addresses 转换为 endpoints（使用辅助函数）
		allEndpoints := convertAddressesToEndpoints(addresses)

		// 2. 构建现有 slices 的映射（按名称索引，方便查找和复用）
		existingSliceMap := make(map[string]*discoveryv1.EndpointSlice)
		for i := range existingSlices {
			existingSliceMap[existingSlices[i].Name] = &existingSlices[i]
		}

		// Rebalance 逻辑：基于最新的 addresses 重新分配
		// 优化策略：最小化修改原则
		// 1. 复用现有的 slices（如果编号匹配且可以更新）
		// 2. 只删除多余的 slices（新分配方案中不需要的）
		// 3. 只创建新分配方案中需要但现有 slices 无法覆盖的部分

		// 构建需要同步的 slices（复用现有的或创建新的）
		for i := 0; i < numSlicesNeeded; i++ {
			start := i * maxEndpointsPerSlice
			end := start + maxEndpointsPerSlice
			if end > len(allEndpoints) {
				end = len(allEndpoints)
			}

			sliceName := fmt.Sprintf("%s-%d", cfg.Name, i)

			var slice *discoveryv1.EndpointSlice
			// 检查是否可以复用现有的 slice
			if existingSlice, exists := existingSliceMap[sliceName]; exists {
				// 复用现有的 slice，更新其 endpoints
				slice = existingSlice.DeepCopy()
				// 从映射中移除，表示已处理
				delete(existingSliceMap, sliceName)
			} else {
				// 需要创建新的 slice
				slice = &discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{
						Name: sliceName,
					},
				}
			}

			// 设置公共属性（无论是复用还是新建）
			slice.Endpoints = allEndpoints[start:end]
			slice.AddressType = discoveryv1.AddressTypeIPv4
			slice.Ports = commonEndpointPorts

			result.SlicesToSync = append(result.SlicesToSync, slice)
		}

		// 收集需要删除的 slices（只删除新分配方案中不需要的）
		// existingSliceMap 中剩余的就是需要删除的
		for sliceName := range existingSliceMap {
			result.SlicesToDelete[sliceName] = struct{}{}
		}

		result.TotalSlices = numSlicesNeeded
		logger.Infof("[kubelet-endpointslice] rebalance plan: total_slices=%d, slices_to_sync=%d, slices_to_delete=%d",
			numSlicesNeeded, len(result.SlicesToSync), len(result.SlicesToDelete))
		return result, nil
	}

	if existingTotalCapacity > 0 {
		logger.Debugf("[kubelet-endpointslice] no rebalance needed: existing slices=%d, needed=%d, capacity usage %.2f%% (%d/%d), threshold %.2f%%",
			existingSliceCount, numSlicesNeeded, float64(actualUsed)/float64(existingTotalCapacity)*100, actualUsed, existingTotalCapacity, rebalanceThreshold*100)
	} else {
		logger.Debugf("[kubelet-endpointslice] no rebalance needed: no existing slices")
	}

	// ========== 第三步：正常分析流程（非 rebalance 场景）==========

	// 删除不再需要的 endpoints（当节点减少时）
	// 构建期望的地址集合（基于 IP 地址，因为节点名称可能变化但 IP 不变）
	desiredIPs := make(map[string]struct{})
	for _, addr := range addresses {
		desiredIPs[addr.IP] = struct{}{}
	}

	// ========== 快速判断：检查是否有变更 ==========
	// 构建现有的地址集合（从现有 slices 中提取所有 IP）
	existingIPsForCheck := make(map[string]struct{})
	for _, slice := range existingSlices {
		for _, ep := range slice.Endpoints {
			if len(ep.Addresses) > 0 {
				existingIPsForCheck[ep.Addresses[0]] = struct{}{}
			}
		}
	}

	// 快速判断：如果期望的 IPs 和现有的 IPs 完全一致，则无需任何变更
	// 条件：数量相同，且所有期望的 IP 都在现有集合中
	if len(desiredIPs) == len(existingIPsForCheck) {
		hasChange := false
		for ip := range desiredIPs {
			if _, exists := existingIPsForCheck[ip]; !exists {
				hasChange = true
				break
			}
		}
		if !hasChange {
			// 无变更，直接返回空结果（不需要删除、不需要同步）
			logger.Debugf("[kubelet-endpointslice] no changes detected, skipping sync (addresses=%d, existing_slices=%d)", len(addresses), len(existingSlices))
			result.TotalSlices = len(existingSlices)
			return result, nil
		}
	}
	logger.Debugf("[kubelet-endpointslice] changes detected, proceeding with analysis (desired=%d, existing=%d)", len(desiredIPs), len(existingIPsForCheck))

	// 从现有 slices 中删除不再需要的 endpoints，并标记是否有删除
	type sliceState struct {
		slice   *discoveryv1.EndpointSlice
		changed bool // 是否有变更（删除或新增）
	}
	slicesCopy := make([]sliceState, 0, len(existingSlices))

	for i := range existingSlices {
		slice := existingSlices[i].DeepCopy()
		originalLen := len(slice.Endpoints)
		// 过滤掉已删除节点的 endpoints
		newEndpoints := make([]discoveryv1.Endpoint, 0, len(slice.Endpoints))
		for _, ep := range slice.Endpoints {
			if len(ep.Addresses) > 0 {
				if _, exists := desiredIPs[ep.Addresses[0]]; exists {
					newEndpoints = append(newEndpoints, ep)
				}
			}
		}

		slice.Endpoints = newEndpoints
		// 如果有 endpoint 被删除，标记为已变更
		// 注意：空 slice 也会加入 slicesCopy，后续删除逻辑会处理
		slicesCopy = append(slicesCopy, sliceState{slice, len(newEndpoints) != originalLen})
	}

	// 按 endpoints 数量降序排序，优先填充最满的 slice
	sort.Slice(slicesCopy, func(i, j int) bool {
		return len(slicesCopy[i].slice.Endpoints) > len(slicesCopy[j].slice.Endpoints)
	})

	// 收集需要新增的 addresses
	remainingAddresses := make([]corev1.EndpointAddress, 0)
	for _, addr := range addresses {
		if _, exists := existingIPsForCheck[addr.IP]; !exists {
			remainingAddresses = append(remainingAddresses, addr)
		}
	}

	// 填充已有 slices 的空位，并检测变更
	// 注意：空 slice 也参与填充，填充后变非空则更新（而不是删除+新建）
	for i := range slicesCopy {
		slice := slicesCopy[i].slice
		if space := maxEndpointsPerSlice - len(slice.Endpoints); space > 0 && len(remainingAddresses) > 0 {
			toAdd := min(space, len(remainingAddresses))
			slice.Endpoints = append(slice.Endpoints, convertAddressesToEndpoints(remainingAddresses[:toAdd])...)
			remainingAddresses = remainingAddresses[toAdd:]
			slicesCopy[i].changed = true // 有新增，标记为已变更
		}
		// 只有有变更（删除或新增）且非空才需要同步
		// 空 slice 会在后面被标记为删除
		if slicesCopy[i].changed && len(slice.Endpoints) > 0 {
			result.SlicesToSync = append(result.SlicesToSync, slice)
		}
	}

	// 如果还有剩余，创建新的 slices（见缝插针策略）
	// 计算需要创建的新 slice 数量（向上取整）
	numNewSlices := (len(remainingAddresses) + maxEndpointsPerSlice - 1) / maxEndpointsPerSlice

	// 计算新 slice 的起始编号（从现有 slices 的最大编号 + 1 开始）
	// 这样可以避免命名冲突，同时保持编号递增（虽然可能不连续，但不影响功能）
	maxExistingIndex := -1
	for _, slice := range existingSlices {
		// 从 slice 名称中提取编号（格式：{service-name}-{index}）
		// 例如：bkmonitor-operator-stack-kubelet-5 -> 5
		// 注意：如果 slice 名称格式不对（如不包含编号），会忽略该 slice，不影响逻辑
		parts := strings.Split(slice.Name, "-")
		if len(parts) > 0 {
			var index int
			_, err := fmt.Sscanf(parts[len(parts)-1], "%d", &index)
			// 如果解析成功且编号大于当前最大值，更新最大值
			// 如果解析失败（slice 名称格式不对），忽略该 slice
			if err == nil && index > maxExistingIndex {
				maxExistingIndex = index
			}
		}
	}
	nextSliceIndex := maxExistingIndex + 1

	// 构建需要新建的 slices（使用递增编号，从 nextSliceIndex 开始）
	for i := 0; i < numNewSlices; i++ {
		// 计算当前 EndpointSlice 的地址范围
		start := i * maxEndpointsPerSlice
		end := start + maxEndpointsPerSlice
		if end > len(remainingAddresses) {
			end = len(remainingAddresses) // 最后一个 slice 可能不足 maxEndpointsPerSlice 个
		}

		// EndpointSlice 命名规则：{service-name}-{index}
		// 例如：bkmonitor-operator-stack-kubelet-0, bkmonitor-operator-stack-kubelet-1
		// 注意：使用递增编号，避免命名冲突（编号可能不连续，但不影响功能）
		sliceName := fmt.Sprintf("%s-%d", cfg.Name, nextSliceIndex+i)

		// 构建当前 slice 的 endpoints 列表（使用辅助函数）
		endpoints := convertAddressesToEndpoints(remainingAddresses[start:end])

		// 构建 EndpointSlice 对象（注意：这里不设置 Namespace、Labels、OwnerReferences，在执行阶段设置）

		result.SlicesToSync = append(result.SlicesToSync, &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: sliceName,
			},
			AddressType: discoveryv1.AddressTypeIPv4, // 仅支持 IPv4
			Endpoints:   endpoints,
			Ports:       commonEndpointPorts, // 使用公共的 Ports 配置
		})
	}

	// 计算需要保留的 slice 数量（有 endpoints 的现有 slice + 新建的 slice）
	validSliceCount := 0
	for _, item := range slicesCopy {
		if len(item.slice.Endpoints) > 0 {
			validSliceCount++
		} else {
			// 空 slice 需要删除
			result.SlicesToDelete[item.slice.Name] = struct{}{}
		}
	}
	result.TotalSlices = validSliceCount + numNewSlices

	return result, nil
}

// executeEndpointSliceChanges 根据分析结果执行删除、更新、新建操作
//
// 功能说明：
// 1. 删除不再需要的 slices（所有场景都需要）
// 2. 如果 Service 不存在（analysisResult.Service == nil），只删除，不更新或创建
// 3. 正常场景下：
//   - 同步需要同步的 slices（包括更新和新建，统一使用 CreateOrUpdateEndpointSlice）
//   - 底层函数会自动判断是创建还是更新，并检查是否有变化，避免不必要的 API 调用
//
// 参数：
//   - ctx: 上下文
//   - endpointSliceClient: EndpointSlice 客户端
//   - cfg: Kubelet 配置
//   - analysisResult: 分析结果（包含 Service 信息，如果 Service 不存在则为 nil）
//
// 返回：
//   - error: 如果执行失败返回错误
func (c *Operator) executeEndpointSliceChanges(ctx context.Context, endpointSliceClient discoveryv1iface.EndpointSliceInterface, cfg configs.Kubelet, analysisResult *endpointSliceAnalysisResult) error {
	// ========== 第一步：删除不再需要的 slices ==========
	logger.Debugf("[kubelet-endpointslice] deleting %d unnecessary endpoint slices", len(analysisResult.SlicesToDelete))
	if err := c.deleteUnnecessaryEndpointSlices(ctx, endpointSliceClient, analysisResult.SlicesToDelete); err != nil {
		logger.Errorf("[kubelet-endpointslice] failed to delete unnecessary endpoint slices: %v", err)
		return errors.Wrap(err, "failed to delete unnecessary endpoint slices")
	}

	// Service 不存在时，只需要删除，不需要更新或创建
	if analysisResult.Service == nil {
		logger.Debugf("[kubelet-endpointslice] service not found, skipping sync (only deletion performed)")
		return nil
	}

	// ========== 第二步：同步 slices（包括更新和新建）==========
	// 注意：底层函数 CreateOrUpdateEndpointSlice 已经支持创建或更新，所以可以统一处理
	logger.Debugf("[kubelet-endpointslice] syncing %d endpoint slices", len(analysisResult.SlicesToSync))

	for i, slice := range analysisResult.SlicesToSync {
		// 设置完整的元数据
		slice.Namespace = cfg.Namespace
		slice.Labels = kubeletServiceLabels.Labels()
		slice.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: "v1",
				Kind:       "Service",
				Name:       cfg.Name,
				UID:        analysisResult.Service.UID,
			},
		}

		// 同步 EndpointSlice（CreateOrUpdateEndpointSlice 会自动判断是创建还是更新）
		// 注意：DeepEqual 检查在 CreateOrUpdateEndpointSlice 内部完成，避免不必要的更新
		logger.Debugf("[kubelet-endpointslice] syncing slice %d/%d: name=%s, endpoints=%d", i+1, len(analysisResult.SlicesToSync), slice.Name, len(slice.Endpoints))
		err := k8sutils.CreateOrUpdateEndpointSlice(ctx, endpointSliceClient, slice)
		if err != nil {
			logger.Errorf("[kubelet-endpointslice] failed to sync endpoint slice %s: %v", slice.Name, err)
			return errors.Wrapf(err, "failed to sync endpoint slice %s", slice.Name)
		}
		logger.Debugf("[kubelet-endpointslice] successfully synced slice: name=%s", slice.Name)
	}

	logger.Infof("[kubelet-endpointslice] successfully synced %d endpoint slices", len(analysisResult.SlicesToSync))

	return nil
}

// deleteUnnecessaryEndpointSlices 删除不再需要的 EndpointSlice
//
// 参数：
//   - ctx: 上下文
//   - endpointSliceClient: EndpointSlice 客户端
//   - slicesToDelete: 需要删除的 slice 名称集合（map[string]struct{}）
//
// 返回：
//   - error: 如果删除失败返回错误（NotFound 错误会被忽略）
func (c *Operator) deleteUnnecessaryEndpointSlices(ctx context.Context, endpointSliceClient discoveryv1iface.EndpointSliceInterface, slicesToDelete map[string]struct{}) error {
	if len(slicesToDelete) == 0 {
		logger.Debugf("[kubelet-endpointslice] no endpoint slices to delete")
		return nil
	}

	logger.Infof("[kubelet-endpointslice] deleting %d unnecessary endpoint slices", len(slicesToDelete))

	var deleteErrors []error
	for sliceName := range slicesToDelete {
		logger.Debugf("[kubelet-endpointslice] deleting slice: name=%s", sliceName)
		err := endpointSliceClient.Delete(ctx, sliceName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err) {
			// 如果删除失败且不是 NotFound 或 Forbidden 错误，记录错误
			// NotFound 错误可以忽略（可能已经被其他进程删除）
			// Forbidden 错误可以忽略（可能是非纳管的资源，没有删除权限）
			deleteErrors = append(deleteErrors, errors.Wrapf(err, "failed to delete endpoint slice %s", sliceName))
			logger.Errorf("[kubelet-endpointslice] failed to delete slice %s: %v", sliceName, err)
		} else if err == nil {
			logger.Debugf("[kubelet-endpointslice] successfully deleted slice: name=%s", sliceName)
		} else if apierrors.IsNotFound(err) {
			logger.Debugf("[kubelet-endpointslice] slice %s already deleted (NotFound)", sliceName)
		} else if apierrors.IsForbidden(err) {
			logger.Warnf("[kubelet-endpointslice] slice %s is forbidden to delete (non-managed resource), skipping", sliceName)
		}
	}

	if len(deleteErrors) > 0 {
		return errors.Wrapf(errors.New("failed to delete some endpoint slices"), "errors: %v", deleteErrors)
	}

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
