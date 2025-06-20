// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package objectsref

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodeMap(t *testing.T) {
	nm := NewNodeMap()

	type Node struct {
		Name string
		Addr string
	}

	nodes := []Node{
		{Name: "node1", Addr: "127.0.0.1"},
		{Name: "node2", Addr: "127.0.0.2"},
		{Name: "node3", Addr: "127.0.0.3"},
	}

	for _, node := range nodes {
		err := nm.Set(&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: node.Name,
			},
			Status: corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeInternalIP,
						Address: node.Addr,
					},
				},
			},
		})
		assert.NoError(t, err)
	}

	assert.Equal(t, 3, nm.Count())

	_, ok := nm.CheckName("node1")
	assert.True(t, ok)
	_, ok = nm.CheckName("node4")
	assert.False(t, ok)

	nm.Del("node1")
	_, ok = nm.CheckName("node1")
	assert.False(t, ok)
}
