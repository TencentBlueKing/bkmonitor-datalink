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
	"context"
	"errors"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
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

func (n *NodeMap) NodeLabels(name string) map[string]string {
	n.mut.Lock()
	defer n.mut.Unlock()

	node, ok := n.nodes[name]
	if !ok {
		return nil
	}

	cloned := make(map[string]string)
	for k, v := range node.Labels {
		cloned[k] = v
	}
	return cloned
}

func (n *NodeMap) CheckIP(s string) bool {
	n.mut.Lock()
	defer n.mut.Unlock()

	for _, ips := range n.ips {
		for _, ip := range ips {
			if s == ip {
				return true
			}
		}
	}

	return false
}

func (n *NodeMap) CheckName(name string) (string, bool) {
	n.mut.Lock()
	defer n.mut.Unlock()

	// 先判断 nodename 是否存在
	node, ok := n.nodes[name]
	if ok {
		// 存在且没有 ignore 配置 直接返回
		if len(configs.G().DaemonSetWorkerIgnoreNodeLabels) == 0 {
			return name, true
		}

		matched := utils.MatchSubLabels(configs.G().DaemonSetWorkerIgnoreNodeLabels, node.Labels)
		if matched {
			return name, false
		}
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
	delete(n.priorityIPs, nodeName)
}

func newNodeObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*NodeMap, error) {
	genericInformer, err := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource(resourceNodes))
	if err != nil {
		return nil, err
	}
	objs := NewNodeMap()

	informer := genericInformer.Informer()
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			node, ok := obj.(*corev1.Node)
			if !ok {
				logger.Errorf("excepted Node type, got %T", obj)
				return
			}
			if err := objs.Set(node); err != nil {
				logger.Errorf("failed to set node obj: %v", err)
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			node, ok := newObj.(*corev1.Node)
			if !ok {
				logger.Errorf("excepted Node type, got %T", newObj)
				return
			}
			if err := objs.Set(node); err != nil {
				logger.Errorf("failed to set node obj: %v", err)
			}
		},
		DeleteFunc: func(obj interface{}) {
			node, ok := obj.(*corev1.Node)
			if !ok {
				logger.Errorf("excepted Node type, got %T", obj)
				return
			}
			objs.Del(node.Name)
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindNode, informer)
	if !synced {
		return nil, errors.New("failed to sync Node caches")
	}
	return objs, nil
}
