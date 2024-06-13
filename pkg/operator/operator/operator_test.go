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
	"reflect"
	"testing"

	"github.com/kylelemons/godebug/pretty"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetNodeAddresses(t *testing.T) {
	cases := []struct {
		name              string
		nodes             *corev1.NodeList
		expectedAddresses []string
		expectedErrors    int
	}{
		{
			name: "simple",
			nodes: &corev1.NodeList{
				Items: []corev1.Node{
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
			},
			expectedAddresses: []string{"127.0.0.1"},
			expectedErrors:    0,
		},
		{
			name: "missing ip on one node",
			nodes: &corev1.NodeList{
				Items: []corev1.Node{
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

func TestParseSelector(t *testing.T) {
	cases := []struct {
		input  string
		output map[string]string
	}{
		{
			input: "__meta_kubernetes_endpoint_address_target_name=^eklet-.*,__meta_kubernetes_endpoint_address_target_kind=Node",
			output: map[string]string{
				"__meta_kubernetes_endpoint_address_target_name": "^eklet-.*",
				"__meta_kubernetes_endpoint_address_target_kind": "Node",
			},
		},
		{
			input: "__meta_kubernetes_endpoint_address_target_name=^eklet-.*,,",
			output: map[string]string{
				"__meta_kubernetes_endpoint_address_target_name": "^eklet-.*",
			},
		},
		{
			input: "foo=bar, , ,k1=v1 ",
			output: map[string]string{
				"foo": "bar",
				"k1":  "v1",
			},
		},
	}

	for _, c := range cases {
		assert.Equal(t, c.output, parseSelector(c.input))
	}
}
