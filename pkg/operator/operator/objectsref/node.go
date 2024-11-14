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
	"errors"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
)

type NodeMap struct {
	mut         sync.Mutex
	nodes       map[string]*corev1.Node
	ips         map[string][]string
	priorityIPs map[string]string
}

func NewNodeMap() *NodeMap {
	return &NodeMap{
		nodes:       map[string]*corev1.Node{},
		ips:         map[string][]string{},
		priorityIPs: map[string]string{},
	}
}

func (n *NodeMap) GetAll() []*corev1.Node {
	n.mut.Lock()
	defer n.mut.Unlock()

	ret := make([]*corev1.Node, 0, len(n.nodes))
	for _, node := range n.nodes {
		ret = append(ret, node)
	}
	return ret
}

func (n *NodeMap) Count() int {
	n.mut.Lock()
	defer n.mut.Unlock()

	return len(n.nodes)
}

func (n *NodeMap) Addrs() map[string]string {
	n.mut.Lock()
	defer n.mut.Unlock()

	cloned := make(map[string]string)
	for k, v := range n.priorityIPs {
		cloned[k] = v
	}
	return cloned
}

func (n *NodeMap) IPs() map[string]struct{} {
	n.mut.Lock()
	defer n.mut.Unlock()

	ret := make(map[string]struct{})
	for _, ips := range n.ips {
		for _, ip := range ips {
			ret[ip] = struct{}{}
		}
	}
	return ret
}

func (n *NodeMap) NameExists(name string) (string, bool) {
	n.mut.Lock()
	defer n.mut.Unlock()

	// 先判断 nodename 是否存在
	if _, ok := n.nodes[name]; ok {
		return name, true
	}

	// 如果不存在的话再判断 nodename 是否为格式化 ip
	name = strings.ReplaceAll(name, "-", ".")
	for nodeName, ip := range n.ips {
		for _, addr := range ip {
			if addr == name {
				return nodeName, true
			}
		}
	}
	return "", false
}

func (n *NodeMap) Names() []string {
	n.mut.Lock()
	defer n.mut.Unlock()

	var nodes []string
	for node := range n.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

func (n *NodeMap) Set(node *corev1.Node) error {
	n.mut.Lock()
	defer n.mut.Unlock()

	if node.Name == "" {
		return errors.New("empty node name")
	}

	n.nodes[node.Name] = node
	priorityIP, address, err := k8sutils.GetNodeAddress(*node)
	if err != nil {
		return err
	}
	n.priorityIPs[node.Name] = priorityIP

	lst := make([]string, 0)
	for _, ips := range address {
		lst = append(lst, ips...)
	}
	n.ips[node.Name] = lst
	return nil
}

func (n *NodeMap) Del(nodeName string) {
	n.mut.Lock()
	defer n.mut.Unlock()

	delete(n.nodes, nodeName)
	delete(n.ips, nodeName)
}
