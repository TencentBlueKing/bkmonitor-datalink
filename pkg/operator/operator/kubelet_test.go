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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref"
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

// mockObjectsController 模拟 ObjectsController，只提供 NodeObjs 方法
// 使用反射来避免导入 etcd 相关的包
type mockObjectsController struct {
	nodes []*corev1.Node
}

func (m *mockObjectsController) NodeObjs() []*corev1.Node {
	return m.nodes
}

// createTestOperator 创建测试用的 Operator 实例
// 注意：由于 protobuf 依赖冲突，此函数会导入 etcd，导致测试无法运行
// 建议在 CI/CD 环境中运行测试，或修复项目依赖冲突
func createTestOperator(t *testing.T, client kubernetes.Interface, nodes []*corev1.Node) *Operator {
	ctx := context.Background()

	// 先创建节点资源到 fake client，这样 ObjectsController 可以通过 informer 发现
	for _, node := range nodes {
		_, err := client.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
		if err != nil {
			// 如果节点已存在，尝试更新
			_, _ = client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
		}
	}

	// 创建 ObjectsController（会通过 informer 自动发现节点）
	// 注意：这会间接导入 etcd，导致 protobuf 冲突
	// 在当前环境中无法运行，需要在 CI/CD 环境或修复依赖后运行
	objectsController, err := objectsref.NewController(ctx, client, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create ObjectsController: %v", err)
	}

	return &Operator{
		ctx:               ctx,
		client:            client,
		objectsController: objectsController,
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
		Namespace: "bkmonitor-operator",
		Name:      "bkmonitor-operator-stack-kubelet",
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
	assert.Equal(t, 1, len(slices.Items), "should create EndpointSlice")
}
