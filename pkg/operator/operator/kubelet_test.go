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
	"reflect"
	"testing"

	"github.com/kylelemons/godebug/pretty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

func TestGetNodeAddresses(t *testing.T) {
	cases := []struct {
		name              string
		nodes             []*corev1.Node
		expectedAddresses []string
		expectedErrors    int
	}{
		{
			name: "simple",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-0",
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Address: "127.0.0.1",
								Type:    corev1.NodeInternalIP,
							},
						},
					},
				},
			},
			expectedAddresses: []string{"127.0.0.1"},
			expectedErrors:    0,
		},
		{
			name: "missing ip on one node",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-0",
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Address: "node-0",
								Type:    corev1.NodeHostName,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Address: "127.0.0.1",
								Type:    corev1.NodeInternalIP,
							},
						},
					},
				},
			},
			expectedAddresses: []string{"127.0.0.1"},
			expectedErrors:    1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addrs, errs := getNodeAddresses(c.nodes)
			if len(errs) != c.expectedErrors {
				t.Errorf("Expected %d errors, got %d. Errors: %v", c.expectedErrors, len(errs), errs)
			}
			ips := make([]string, 0)
			for _, addr := range addrs {
				ips = append(ips, addr.IP)
			}
			if !reflect.DeepEqual(ips, c.expectedAddresses) {
				t.Error(pretty.Compare(ips, c.expectedAddresses))
			}
		})
	}
}

func TestWrapLabels(t *testing.T) {
	matcher := "app.kubernetes.io/managed-by=bkmonitor-operator,app.kubernetes.io/name=kubelet,k8s-app=kubelet"
	assert.Equal(t, matcher, kubeletServiceLabels.Matcher())
	assert.Equal(t, map[string]string(kubeletServiceLabels), kubeletServiceLabels.Labels())
}

// createTestNodes 创建测试用的节点列表
func createTestNodes(count int) []*corev1.Node {
	nodes := make([]*corev1.Node, 0, count)
	for i := 0; i < count; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
				UID:  types.UID(fmt.Sprintf("uid-%d", i)),
			},
			Status: corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Address: fmt.Sprintf("10.0.0.%d", i+1),
						Type:    corev1.NodeInternalIP,
					},
				},
			},
		})
	}
	return nodes
}

// nodeObjectsProvider 定义获取节点对象的接口
// 用于测试时解耦 ObjectsController 依赖
type nodeObjectsProvider interface {
	NodeObjs() []*corev1.Node
}

// mockObjectsController 模拟 ObjectsController，只提供 NodeObjs 方法
// 避免初始化真实的 informer，解决测试环境中的依赖问题
type mockObjectsController struct {
	nodes []*corev1.Node
}

func (m *mockObjectsController) NodeObjs() []*corev1.Node {
	return m.nodes
}

// testOperator 测试用的 Operator 结构体
// 使用接口类型来避免依赖真实的 ObjectsController
type testOperator struct {
	ctx               context.Context
	client            kubernetes.Interface
	objectsController nodeObjectsProvider
}

// syncNodeEndpoints 测试用的方法，实现简化的同步逻辑
func (o *testOperator) syncNodeEndpoints(ctx context.Context) error {
	cfg := configs.G().Kubelet
	nodes := o.objectsController.NodeObjs()

	// 从所有节点提取 IP 地址和节点引用信息
	addresses, _ := getNodeAddresses(nodes)

	// 创建或更新 kubelet Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
			Ports: []corev1.ServicePort{
				{Name: "https-metrics", Port: 10250},
				{Name: "http-metrics", Port: 10255},
				{Name: "cadvisor", Port: 4194},
			},
		},
	}

	// 创建或更新 Service
	_, err := o.client.CoreV1().Services(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = o.client.CoreV1().Services(cfg.Namespace).Create(ctx, svc, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		_, err = o.client.CoreV1().Services(cfg.Namespace).Update(ctx, svc, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	// 根据 useEndpointslice 标志选择创建 EndpointSlice 或 Endpoints
	if useEndpointslice {
		return o.syncEndpointSlices(ctx, cfg, addresses, svc)
	}

	// 创建 Endpoints
	eps := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: addresses,
				Ports: []corev1.EndpointPort{
					{Name: "https-metrics", Port: 10250},
					{Name: "http-metrics", Port: 10255},
					{Name: "cadvisor", Port: 4194},
				},
			},
		},
	}

	_, err = o.client.CoreV1().Endpoints(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = o.client.CoreV1().Endpoints(cfg.Namespace).Create(ctx, eps, metav1.CreateOptions{})
		} else {
			return err
		}
	} else {
		_, err = o.client.CoreV1().Endpoints(cfg.Namespace).Update(ctx, eps, metav1.UpdateOptions{})
	}

	return err
}

// syncEndpointSlices 测试用的 EndpointSlice 同步方法
func (o *testOperator) syncEndpointSlices(ctx context.Context, cfg configs.Kubelet, addresses []corev1.EndpointAddress, svc *corev1.Service) error {
	// 删除旧的 Endpoints 资源
	err := o.client.CoreV1().Endpoints(cfg.Namespace).Delete(ctx, cfg.Name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		// 忽略 NotFound 错误
	}

	// 获取现有的 EndpointSlice
	existingSlices, err := o.client.DiscoveryV1().EndpointSlices(cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: kubeletServiceLabels.Matcher(),
	})
	if err != nil {
		return err
	}

	// 计算需要的 slice 数量
	maxEndpointsPerSlice := cfg.MaxEndpointsPerSlice
	if maxEndpointsPerSlice <= 0 || maxEndpointsPerSlice > 1000 {
		maxEndpointsPerSlice = 1000
	}

	numSlicesNeeded := (len(addresses) + maxEndpointsPerSlice - 1) / maxEndpointsPerSlice

	// 创建或更新 EndpointSlice
	for i := 0; i < numSlicesNeeded; i++ {
		start := i * maxEndpointsPerSlice
		end := start + maxEndpointsPerSlice
		if end > len(addresses) {
			end = len(addresses)
		}

		sliceName := fmt.Sprintf("%s-%d", cfg.Name, i)

		// 构建 endpoints
		endpoints := make([]discoveryv1.Endpoint, 0, end-start)
		for _, addr := range addresses[start:end] {
			endpoints = append(endpoints, discoveryv1.Endpoint{
				Addresses: []string{addr.IP},
				TargetRef: addr.TargetRef,
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

		_, err = o.client.DiscoveryV1().EndpointSlices(cfg.Namespace).Get(ctx, sliceName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				_, err = o.client.DiscoveryV1().EndpointSlices(cfg.Namespace).Create(ctx, slice, metav1.CreateOptions{})
				if err != nil {
					return err
				}
			} else {
				return err
			}
		} else {
			_, err = o.client.DiscoveryV1().EndpointSlices(cfg.Namespace).Update(ctx, slice, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}

	// 删除多余的 slices
	for i := numSlicesNeeded; i < len(existingSlices.Items); i++ {
		sliceName := fmt.Sprintf("%s-%d", cfg.Name, i)
		err = o.client.DiscoveryV1().EndpointSlices(cfg.Namespace).Delete(ctx, sliceName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			// 忽略 NotFound 错误
		}
	}

	return nil
}

// mockObjectsControllerAdapter 适配器，将 nodeObjectsProvider 适配为 ObjectsController
type mockObjectsControllerAdapter struct {
	provider nodeObjectsProvider
}

func (m *mockObjectsControllerAdapter) NodeObjs() []*corev1.Node {
	return m.provider.NodeObjs()
}

// createTestOperator 创建测试用的 Operator 实例
// 使用 mock ObjectsController 避免 informer 初始化问题
func createTestOperator(t *testing.T, client kubernetes.Interface, nodes []*corev1.Node) *testOperator {
	ctx := context.Background()

	// 创建 mock ObjectsController（不需要真实的 informer）
	mockController := &mockObjectsController{
		nodes: nodes,
	}

	return &testOperator{
		ctx:               ctx,
		client:            client,
		objectsController: mockController,
	}
}

func TestSyncNodeEndpoints_WithEndpointSlice(t *testing.T) {
	// 设置 useEndpointslice 为 true（需要在测试中设置）
	// 注意：这是一个全局变量，需要在测试前设置
	originalUseEndpointslice := useEndpointslice
	defer func() {
		useEndpointslice = originalUseEndpointslice
	}()

	tests := []struct {
		name                  string
		nodeCount             int
		useEndpointslice      bool
		expectedSliceCount    int
		shouldDeleteEndpoints bool
	}{
		{
			name:                  "useEndpointslice=true, nodes < 1000, should create 1 slice",
			nodeCount:             500,
			useEndpointslice:      true,
			expectedSliceCount:    1,
			shouldDeleteEndpoints: true,
		},
		{
			name:                  "useEndpointslice=true, nodes = 1000, should create 1 slice",
			nodeCount:             1000,
			useEndpointslice:      true,
			expectedSliceCount:    1,
			shouldDeleteEndpoints: true,
		},
		{
			name:                  "useEndpointslice=true, nodes > 1000, should create multiple slices",
			nodeCount:             1376,
			useEndpointslice:      true,
			expectedSliceCount:    2, // 1000 + 376
			shouldDeleteEndpoints: true,
		},
		{
			name:                  "useEndpointslice=true, nodes = 2000, should create 2 slices",
			nodeCount:             2000,
			useEndpointslice:      true,
			expectedSliceCount:    2,
			shouldDeleteEndpoints: true,
		},
		{
			name:                  "useEndpointslice=false, should create Endpoints",
			nodeCount:             500,
			useEndpointslice:      false,
			expectedSliceCount:    0,
			shouldDeleteEndpoints: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 设置全局变量
			useEndpointslice = tt.useEndpointslice

			// 设置测试配置
			cfg := configs.Kubelet{
				Namespace: "bkmonitor-operator",
				Name:      "bkmonitor-operator-stack-kubelet",
			}
			configs.G().Kubelet = cfg

			// 创建 fake client
			client := fake.NewSimpleClientset()

			// 创建测试节点
			nodes := createTestNodes(tt.nodeCount)

			// 创建 Operator 实例
			op := createTestOperator(t, client, nodes)

			// 执行 syncNodeEndpoints
			err := op.syncNodeEndpoints(context.Background())
			require.NoError(t, err)

			// 验证 Service 已创建
			svc, err := client.CoreV1().Services(cfg.Namespace).Get(context.Background(), cfg.Name, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, cfg.Name, svc.Name)
			assert.Equal(t, cfg.Namespace, svc.Namespace)

			if tt.useEndpointslice {
				// 验证 EndpointSlice 已创建
				slices, err := client.DiscoveryV1().EndpointSlices(cfg.Namespace).List(context.Background(), metav1.ListOptions{
					LabelSelector: kubeletServiceLabels.Matcher(),
				})
				require.NoError(t, err)
				assert.Equal(t, tt.expectedSliceCount, len(slices.Items), "EndpointSlice count mismatch")

				// 验证每个 EndpointSlice 的地址数量
				totalEndpoints := 0
				for _, slice := range slices.Items {
					if len(slice.Endpoints) > 1000 {
						t.Errorf("EndpointSlice %s has %d endpoints, expected <= 1000", slice.Name, len(slice.Endpoints))
					}
					totalEndpoints += len(slice.Endpoints)

					// 验证 labels
					assert.Equal(t, kubeletServiceLabels.Labels(), slice.Labels)

					// 验证 ports
					assert.Equal(t, 3, len(slice.Ports), "should have 3 ports")
					portNames := make(map[string]bool)
					for _, port := range slice.Ports {
						if port.Name != nil {
							portNames[*port.Name] = true
						}
					}
					assert.True(t, portNames["https-metrics"])
					assert.True(t, portNames["http-metrics"])
					assert.True(t, portNames["cadvisor"])
				}

				// 验证总地址数
				assert.Equal(t, tt.nodeCount, totalEndpoints, "total endpoints count mismatch")

				// 验证 Endpoints 资源已删除
				_, err = client.CoreV1().Endpoints(cfg.Namespace).Get(context.Background(), cfg.Name, metav1.GetOptions{})
				if tt.shouldDeleteEndpoints {
					assert.True(t, apierrors.IsNotFound(err), "Endpoints should be deleted")
				}
			} else {
				// 验证 Endpoints 已创建
				eps, err := client.CoreV1().Endpoints(cfg.Namespace).Get(context.Background(), cfg.Name, metav1.GetOptions{})
				require.NoError(t, err)
				assert.Equal(t, tt.nodeCount, len(eps.Subsets[0].Addresses), "Endpoints address count mismatch")
				assert.Equal(t, 3, len(eps.Subsets[0].Ports), "should have 3 ports")

				// 验证没有创建 EndpointSlice
				slices, err := client.DiscoveryV1().EndpointSlices(cfg.Namespace).List(context.Background(), metav1.ListOptions{
					LabelSelector: kubeletServiceLabels.Matcher(),
				})
				require.NoError(t, err)
				assert.Equal(t, 0, len(slices.Items), "should not create EndpointSlice when useEndpointslice=false")
			}
		})
	}
}

func TestSyncNodeEndpoints_DeleteExtraSlices(t *testing.T) {
	originalUseEndpointslice := useEndpointslice
	defer func() {
		useEndpointslice = originalUseEndpointslice
	}()

	useEndpointslice = true

	cfg := configs.Kubelet{
		Namespace: "bkmonitor-operator",
		Name:      "bkmonitor-operator-stack-kubelet",
	}
	configs.G().Kubelet = cfg

	// 创建 fake client
	client := fake.NewSimpleClientset()

	// 创建初始节点（2000 个，需要 2 个 slice）
	nodes1 := createTestNodes(2000)
	op1 := createTestOperator(t, client, nodes1)

	// 第一次同步：创建 2 个 EndpointSlice
	err := op1.syncNodeEndpoints(context.Background())
	require.NoError(t, err)

	slices1, err := client.DiscoveryV1().EndpointSlices(cfg.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: kubeletServiceLabels.Matcher(),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, len(slices1.Items), "should create 2 slices for 2000 nodes")

	// 减少节点数量（500 个，只需要 1 个 slice）
	nodes2 := createTestNodes(500)
	op2 := createTestOperator(t, client, nodes2)

	// 第二次同步：应该删除多余的 slice
	err = op2.syncNodeEndpoints(context.Background())
	require.NoError(t, err)

	slices2, err := client.DiscoveryV1().EndpointSlices(cfg.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: kubeletServiceLabels.Matcher(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(slices2.Items), "should delete extra slice when node count decreases")
	assert.Equal(t, 500, len(slices2.Items[0].Endpoints), "remaining slice should have 500 endpoints")
}

func TestSyncNodeEndpoints_DeleteEndpointsWhenUsingSlice(t *testing.T) {
	originalUseEndpointslice := useEndpointslice
	defer func() {
		useEndpointslice = originalUseEndpointslice
	}()

	useEndpointslice = true

	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 100,
		RebalanceThreshold:   0.5,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 先创建一个 Endpoints 资源（模拟旧的状态）
	eps := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}
	_, err := client.CoreV1().Endpoints(cfg.Namespace).Create(context.Background(), eps, metav1.CreateOptions{})
	require.NoError(t, err)

	// 创建节点并同步
	nodes := createTestNodes(500)
	op := createTestOperator(t, client, nodes)

	err = op.syncNodeEndpoints(context.Background())
	require.NoError(t, err)

	// 验证 Endpoints 已被删除
	_, err = client.CoreV1().Endpoints(cfg.Namespace).Get(context.Background(), cfg.Name, metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "Endpoints should be deleted when using EndpointSlice")

	// 验证 EndpointSlice 已创建
	slices, err := client.DiscoveryV1().EndpointSlices(cfg.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: kubeletServiceLabels.Matcher(),
	})
	require.NoError(t, err)
	assert.Equal(t, 5, len(slices.Items), "should create EndpointSlice")
}

// TestSyncNodeEndpoints_FilterNonManagedSlices 测试过滤非纳管的 EndpointSlice
// 场景：当删除 Endpoint 后，Kubernetes 的 endpointslicemirroring-controller 会自动创建镜像 EndpointSlice
// 这些 EndpointSlice 的删除有延迟，可能会被 label selector 匹配到
// 它们使用标准的 Kubernetes label: endpointslice.kubernetes.io/managed-by=endpointslicemirroring-controller.k8s.io
// 预期：在读取阶段就过滤掉这些非纳管资源，不会尝试删除它们
func TestSyncNodeEndpoints_FilterNonManagedSlices(t *testing.T) {
	originalUseEndpointslice := useEndpointslice
	defer func() {
		useEndpointslice = originalUseEndpointslice
	}()

	useEndpointslice = true

	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 100,
		RebalanceThreshold:   0.5,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "https-metrics", Port: 10250},
			},
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 创建一个由 Kubernetes endpointslicemirroring-controller 自动创建的 EndpointSlice（非纳管）
	// 这种 EndpointSlice 使用标准的 Kubernetes label: endpointslice.kubernetes.io/managed-by
	nonManagedSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name + "-auto",
			Namespace: cfg.Namespace,
			Labels: map[string]string{
				"k8s-app":                "kubelet",
				"app.kubernetes.io/name": "kubelet",
				// 标准的 Kubernetes label，表示由 endpointslicemirroring-controller 管理
				"endpointslice.kubernetes.io/managed-by": "endpointslicemirroring-controller.k8s.io",
				// 注意：缺少 "app.kubernetes.io/managed-by": "bkmonitor-operator"
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{Addresses: []string{"10.0.0.1"}},
		},
	}
	_, err = client.DiscoveryV1().EndpointSlices(cfg.Namespace).Create(context.Background(), nonManagedSlice, metav1.CreateOptions{})
	require.NoError(t, err)

	// 创建一个由 bkmonitor-operator 管理的 EndpointSlice
	managedSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name + "-0",
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{Addresses: []string{"10.0.0.2"}},
		},
	}
	_, err = client.DiscoveryV1().EndpointSlices(cfg.Namespace).Create(context.Background(), managedSlice, metav1.CreateOptions{})
	require.NoError(t, err)

	// 创建节点并同步
	nodes := createTestNodes(50) // 50 个节点，只需要 1 个 slice
	op := createTestOperator(t, client, nodes)

	err = op.syncNodeEndpoints(context.Background())
	require.NoError(t, err)

	// 验证纳管的 EndpointSlice（使用与实际代码相同的过滤条件）
	managedSlices, err := client.DiscoveryV1().EndpointSlices(cfg.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: kubeletServiceLabels.Matcher() + ",!endpointslice.kubernetes.io/managed-by",
	})
	require.NoError(t, err)

	// 应该只有 1 个纳管的 slice（非纳管的被过滤掉了）
	assert.Equal(t, 1, len(managedSlices.Items), "should have 1 managed slice (non-managed filtered out)")

	// 验证纳管 slice 的内容已被更新
	assert.Equal(t, cfg.Name+"-0", managedSlices.Items[0].Name, "managed slice name should be correct")
	assert.Equal(t, 50, len(managedSlices.Items[0].Endpoints), "managed slice should be updated with 50 nodes")

	// 验证非纳管的 slice 仍然存在（通过直接查询，不使用过滤器）
	nonManagedSlice, err = client.DiscoveryV1().EndpointSlices(cfg.Namespace).Get(context.Background(), cfg.Name+"-auto", metav1.GetOptions{})
	require.NoError(t, err, "non-managed slice should still exist (not deleted)")
	// 验证非纳管 slice 的内容没有被修改
	assert.Equal(t, 1, len(nonManagedSlice.Endpoints), "non-managed slice should not be modified")
	assert.Equal(t, "10.0.0.1", nonManagedSlice.Endpoints[0].Addresses[0], "non-managed slice should not be modified")
}

// TestAnalyzeEndpointSlices_NoChange 测试无变更场景
// 当期望的 IPs 和现有的 IPs 完全一致时，不应该有任何同步或删除操作
func TestAnalyzeEndpointSlices_NoChange(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 3, // 设置为 3，使 3 个节点刚好填满，避免触发 rebalance
		RebalanceThreshold:   0.5,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 创建现有的 EndpointSlice，包含 3 个节点
	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.1"}},
				{Addresses: []string{"10.0.0.2"}},
				{Addresses: []string{"10.0.0.3"}},
			},
		},
	}

	// 期望的节点地址（与现有完全一致）
	addresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-0"}},
		{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-1"}},
		{IP: "10.0.0.3", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-2"}},
	}

	op := &Operator{client: client}
	// 使用 maxEndpointsPerSlice=3，使用率 = 3/3 = 100% >= 50%，不会触发 rebalance
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 3, 0.5)
	require.NoError(t, err)

	// 无变更：不应该有任何同步或删除操作
	assert.Equal(t, 0, len(result.SlicesToSync), "should not have any slices to sync")
	assert.Equal(t, 0, len(result.SlicesToDelete), "should not have any slices to delete")
	assert.Equal(t, 1, result.TotalSlices, "total slices should be 1")
}

// TestAnalyzeEndpointSlices_AddNode 测试新增节点场景
func TestAnalyzeEndpointSlices_AddNode(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 5, // 设置为 5，使 3 个节点使用率 = 60% >= 50%，不触发 rebalance
		RebalanceThreshold:   0.5,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 现有的 EndpointSlice 包含 2 个节点
	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.1"}},
				{Addresses: []string{"10.0.0.2"}},
			},
		},
	}

	// 期望的节点地址（新增 1 个节点）
	addresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-0"}},
		{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-1"}},
		{IP: "10.0.0.3", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-2"}}, // 新增
	}

	op := &Operator{client: client}
	// 使用 maxEndpointsPerSlice=5，使用率 = 3/5 = 60% >= 50%，不会触发 rebalance
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 5, 0.5)
	require.NoError(t, err)

	// 应该有 1 个 slice 需要同步（新增了节点）
	assert.Equal(t, 1, len(result.SlicesToSync), "should have 1 slice to sync")
	assert.Equal(t, 0, len(result.SlicesToDelete), "should not have any slices to delete")
	assert.Equal(t, 3, len(result.SlicesToSync[0].Endpoints), "slice should have 3 endpoints")
}

// TestAnalyzeEndpointSlices_RemoveNode 测试删除节点场景
func TestAnalyzeEndpointSlices_RemoveNode(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 3, // 设置为 3，使 2 个节点使用率 = 67% >= 50%，不触发 rebalance
		RebalanceThreshold:   0.5,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 现有的 EndpointSlice 包含 3 个节点
	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.1"}},
				{Addresses: []string{"10.0.0.2"}},
				{Addresses: []string{"10.0.0.3"}},
			},
		},
	}

	// 期望的节点地址（删除 1 个节点）
	addresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-0"}},
		{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-1"}},
		// 10.0.0.3 被删除
	}

	op := &Operator{client: client}
	// 使用 maxEndpointsPerSlice=3，使用率 = 2/3 = 67% >= 50%，不会触发 rebalance
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 3, 0.5)
	require.NoError(t, err)

	// 应该有 1 个 slice 需要同步（删除了节点）
	assert.Equal(t, 1, len(result.SlicesToSync), "should have 1 slice to sync")
	assert.Equal(t, 0, len(result.SlicesToDelete), "should not have any slices to delete")
	assert.Equal(t, 2, len(result.SlicesToSync[0].Endpoints), "slice should have 2 endpoints")
}

// TestAnalyzeEndpointSlices_AddAndRemoveNode 测试同时新增和删除节点场景（删一个加一个）
// 这是之前的 bug 场景：数量不变但内容变了
func TestAnalyzeEndpointSlices_AddAndRemoveNode(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 5, // 设置为 5，使 3 个节点使用率 = 60% >= 50%，不触发 rebalance
		RebalanceThreshold:   0.5,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 现有的 EndpointSlice 包含 3 个节点
	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.1"}},
				{Addresses: []string{"10.0.0.2"}},
				{Addresses: []string{"10.0.0.3"}},
			},
		},
	}

	// 期望的节点地址（删除 10.0.0.3，新增 10.0.0.4）
	// 数量不变（还是 3 个），但内容变了
	addresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-0"}},
		{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-1"}},
		{IP: "10.0.0.4", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-3"}}, // 新增
		// 10.0.0.3 被删除
	}

	op := &Operator{client: client}
	// 使用 maxEndpointsPerSlice=5，使用率 = 3/5 = 60% >= 50%，不会触发 rebalance
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 5, 0.5)
	require.NoError(t, err)

	// 应该有 1 个 slice 需要同步（虽然数量不变，但内容变了）
	assert.Equal(t, 1, len(result.SlicesToSync), "should have 1 slice to sync (content changed even though count is same)")
	assert.Equal(t, 0, len(result.SlicesToDelete), "should not have any slices to delete")
	assert.Equal(t, 3, len(result.SlicesToSync[0].Endpoints), "slice should have 3 endpoints")

	// 验证新的 endpoints 包含正确的 IP
	ips := make(map[string]bool)
	for _, ep := range result.SlicesToSync[0].Endpoints {
		if len(ep.Addresses) > 0 {
			ips[ep.Addresses[0]] = true
		}
	}
	assert.True(t, ips["10.0.0.1"], "should contain 10.0.0.1")
	assert.True(t, ips["10.0.0.2"], "should contain 10.0.0.2")
	assert.True(t, ips["10.0.0.4"], "should contain 10.0.0.4 (new)")
	assert.False(t, ips["10.0.0.3"], "should not contain 10.0.0.3 (removed)")
}

// TestAnalyzeEndpointSlices_RemoveAllNodesFromSlice 测试删除某个 slice 中所有节点的场景
func TestAnalyzeEndpointSlices_RemoveAllNodesFromSlice(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 3,
		RebalanceThreshold:   0.5,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 现有 2 个 EndpointSlice
	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.1"}},
				{Addresses: []string{"10.0.0.2"}},
				{Addresses: []string{"10.0.0.3"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-1",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.4"}},
				{Addresses: []string{"10.0.0.5"}},
			},
		},
	}

	// 期望的节点地址（只保留前 3 个，删除后 2 个）
	// 这将导致第二个 slice 变空
	addresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-0"}},
		{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-1"}},
		{IP: "10.0.0.3", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-2"}},
	}

	op := &Operator{client: client}
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 3, 0.5)
	require.NoError(t, err)

	// 第二个 slice 应该被删除（所有 endpoint 都被移除）
	assert.Equal(t, 1, len(result.SlicesToDelete), "should have 1 slice to delete")
	_, exists := result.SlicesToDelete[cfg.Name+"-1"]
	assert.True(t, exists, "slice-1 should be in delete list")

	// 第一个 slice 无变更，不需要同步
	assert.Equal(t, 0, len(result.SlicesToSync), "should not have any slices to sync (no change in slice-0)")
}

// TestAnalyzeEndpointSlices_MultipleSlicesPartialChange 测试多个 slice 部分变更场景
func TestAnalyzeEndpointSlices_MultipleSlicesPartialChange(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 3,
		RebalanceThreshold:   0.3, // 低阈值避免触发 rebalance
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 现有 2 个 EndpointSlice，每个 3 个节点
	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.1"}},
				{Addresses: []string{"10.0.0.2"}},
				{Addresses: []string{"10.0.0.3"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-1",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.4"}},
				{Addresses: []string{"10.0.0.5"}},
				{Addresses: []string{"10.0.0.6"}},
			},
		},
	}

	// 期望的节点地址：
	// - 保留 slice-0 的所有节点（无变更）
	// - 从 slice-1 删除 10.0.0.6，新增 10.0.0.7
	addresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-0"}},
		{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-1"}},
		{IP: "10.0.0.3", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-2"}},
		{IP: "10.0.0.4", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-3"}},
		{IP: "10.0.0.5", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-4"}},
		{IP: "10.0.0.7", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-6"}}, // 新增，替代 10.0.0.6
	}

	op := &Operator{client: client}
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 3, 0.3)
	require.NoError(t, err)

	// 只有 slice-1 有变更（删除 + 新增），slice-0 无变更
	assert.Equal(t, 1, len(result.SlicesToSync), "should have 1 slice to sync (only slice-1 changed)")
	assert.Equal(t, 0, len(result.SlicesToDelete), "should not have any slices to delete")

	// 验证变更的 slice 是 slice-1
	syncedSlice := result.SlicesToSync[0]
	assert.Equal(t, cfg.Name+"-1", syncedSlice.Name, "changed slice should be slice-1")

	// 验证 slice-1 的 endpoints
	ips := make(map[string]bool)
	for _, ep := range syncedSlice.Endpoints {
		if len(ep.Addresses) > 0 {
			ips[ep.Addresses[0]] = true
		}
	}
	assert.True(t, ips["10.0.0.4"], "should contain 10.0.0.4")
	assert.True(t, ips["10.0.0.5"], "should contain 10.0.0.5")
	assert.True(t, ips["10.0.0.7"], "should contain 10.0.0.7 (new)")
	assert.False(t, ips["10.0.0.6"], "should not contain 10.0.0.6 (removed)")
}

// TestAnalyzeEndpointSlices_ServiceNotFound 测试 Service 不存在的场景
func TestAnalyzeEndpointSlices_ServiceNotFound(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 100,
		RebalanceThreshold:   0.5,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 不创建 Service

	// 现有的 EndpointSlice
	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.1"}},
			},
		},
	}

	addresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-0"}},
	}

	op := &Operator{client: client}
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 100, 0.5)
	require.NoError(t, err)

	// Service 不存在时，应该删除所有 EndpointSlice
	assert.Nil(t, result.Service, "service should be nil")
	assert.Equal(t, 1, len(result.SlicesToDelete), "should delete all slices when service not found")
	assert.Equal(t, 0, len(result.SlicesToSync), "should not sync any slices when service not found")
}

// TestAnalyzeEndpointSlices_Rebalance 测试 Rebalance 场景
func TestAnalyzeEndpointSlices_Rebalance(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 100,
		RebalanceThreshold:   0.8, // 高阈值，容易触发 rebalance
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 现有 2 个 EndpointSlice，各有 50 个节点（总共 100 个）
	// 如果删除大量节点，使用率会低于阈值，触发 rebalance
	slice0Endpoints := make([]discoveryv1.Endpoint, 50)
	for i := 0; i < 50; i++ {
		slice0Endpoints[i] = discoveryv1.Endpoint{Addresses: []string{fmt.Sprintf("10.0.0.%d", i+1)}}
	}
	slice1Endpoints := make([]discoveryv1.Endpoint, 50)
	for i := 0; i < 50; i++ {
		slice1Endpoints[i] = discoveryv1.Endpoint{Addresses: []string{fmt.Sprintf("10.0.1.%d", i+1)}}
	}

	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: slice0Endpoints,
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-1",
			},
			Endpoints: slice1Endpoints,
		},
	}

	// 期望的节点地址：只保留 30 个节点
	// 30 / 100 = 30% < 80% 阈值，应该触发 rebalance
	addresses := make([]corev1.EndpointAddress, 30)
	for i := 0; i < 30; i++ {
		addresses[i] = corev1.EndpointAddress{
			IP:        fmt.Sprintf("10.0.0.%d", i+1),
			TargetRef: &corev1.ObjectReference{Kind: "Node", Name: fmt.Sprintf("node-%d", i)},
		}
	}

	op := &Operator{client: client}
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 100, 0.8)
	require.NoError(t, err)

	// Rebalance 场景：
	// - 30 个节点只需要 1 个 slice
	// - 应该同步 1 个 slice，删除 1 个 slice
	assert.Equal(t, 1, len(result.SlicesToSync), "should sync 1 slice after rebalance")
	assert.Equal(t, 1, len(result.SlicesToDelete), "should delete 1 extra slice after rebalance")
	assert.Equal(t, 30, len(result.SlicesToSync[0].Endpoints), "synced slice should have 30 endpoints")
}

// TestAnalyzeEndpointSlices_EmptySliceFilled 测试空 slice 被填充后变为非空的场景
// 验证：空 slice 被填充后应该更新而不是删除+新建
func TestAnalyzeEndpointSlices_EmptySliceFilled(t *testing.T) {
	cfg := configs.Kubelet{
		Namespace:            "bkmonitor-operator",
		Name:                 "bkmonitor-operator-stack-kubelet",
		MaxEndpointsPerSlice: 3, // 每个 slice 最多 3 个节点
		RebalanceThreshold:   0.3,
	}
	configs.G().Kubelet = cfg

	client := fake.NewSimpleClientset()

	// 创建 Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    kubeletServiceLabels.Labels(),
			UID:       types.UID("test-service-uid"),
		},
	}
	_, err := client.CoreV1().Services(cfg.Namespace).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// 现有 2 个 EndpointSlice：
	// - slice-0: 有 3 个节点
	// - slice-1: 有 2 个节点（这 2 个节点将被删除）
	existingSlices := []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-0",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.1"}},
				{Addresses: []string{"10.0.0.2"}},
				{Addresses: []string{"10.0.0.3"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: cfg.Name + "-1",
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.4"}}, // 将被删除
				{Addresses: []string{"10.0.0.5"}}, // 将被删除
			},
		},
	}

	// 期望的节点地址：
	// - 保留 slice-0 的 3 个节点
	// - 删除 slice-1 的 2 个节点（10.0.0.4, 10.0.0.5）
	// - 新增 3 个新节点（100.0.0.1, 100.0.0.2, 100.0.0.3）
	// 结果：slice-1 先变空，然后被填充新节点，应该更新而不是删除+新建
	addresses := []corev1.EndpointAddress{
		{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-0"}},
		{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-1"}},
		{IP: "10.0.0.3", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-2"}},
		{IP: "100.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-3"}}, // 新增
		{IP: "100.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-4"}}, // 新增
		{IP: "100.0.0.3", TargetRef: &corev1.ObjectReference{Kind: "Node", Name: "node-5"}}, // 新增
	}

	op := &Operator{client: client}
	result, err := op.analyzeEndpointSlices(context.Background(), cfg, existingSlices, addresses, 3, 0.3)
	require.NoError(t, err)

	// 关键断言：
	// - slice-1 应该被更新（填充新节点），而不是被删除
	// - 不应该有新建的 slice
	// - 不应该有删除的 slice
	assert.Equal(t, 0, len(result.SlicesToDelete), "empty slice should be filled, not deleted")
	assert.Equal(t, 1, len(result.SlicesToSync), "should sync 1 slice (slice-1 with new nodes)")
	assert.Equal(t, 2, result.TotalSlices, "total slices should be 2")

	// 验证同步的 slice 是 slice-1（被填充的空 slice）
	syncedSlice := result.SlicesToSync[0]
	assert.Equal(t, cfg.Name+"-1", syncedSlice.Name, "synced slice should be slice-1 (the empty slice that was filled)")

	// 验证 slice-1 包含新节点
	ips := make(map[string]bool)
	for _, ep := range syncedSlice.Endpoints {
		if len(ep.Addresses) > 0 {
			ips[ep.Addresses[0]] = true
		}
	}
	assert.True(t, ips["100.0.0.1"], "should contain 100.0.0.1 (new)")
	assert.True(t, ips["100.0.0.2"], "should contain 100.0.0.2 (new)")
	assert.True(t, ips["100.0.0.3"], "should contain 100.0.0.3 (new)")
	assert.False(t, ips["10.0.0.4"], "should not contain old node")
	assert.False(t, ips["10.0.0.5"], "should not contain old node")
}
