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
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	relationNodeSystem           = "node_with_system_relation"
	relationNodePod              = "node_with_pod_relation"
	relationJobPod               = "job_with_pod_relation"
	relationPodReplicaset        = "pod_with_replicaset_relation"
	relationPodStatefulset       = "pod_with_statefulset_relation"
	relationDaemonsetPod         = "daemonset_with_pod_relation"
	relationDeploymentReplicaset = "deployment_with_replicaset_relation"
	relationContainerPod         = "container_with_pod_relation"
	relationPodService           = "pod_with_service_relation"
	relationK8sAddressService    = "k8s_address_with_service_relation"
	relationDomainService        = "domain_with_service_relation"
	relationIngressService       = "ingress_with_service_relation"

	relationContainerWithDataSource   = "container_with_datasource_relation"
	relationDataSourceWithPod         = "datasource_with_pod_relation"
	relationDataSourceWithNode        = "datasource_with_node_relation"
	relationBkLogConfigWithDataSource = "bklogconfig_with_datasource_relation"
)

type relationMetric struct {
	Name   string
	Labels []relationLabel
}

type relationLabel struct {
	Name  string
	Value string
}

func (oc *ObjectsController) GetNodeRelations(w io.Writer) {
	for node, ip := range oc.nodeObjs.Addrs() {
		relationBytes(w, relationMetric{
			Name: relationNodeSystem,
			Labels: []relationLabel{
				{Name: "node", Value: node},
				{Name: "bk_target_ip", Value: ip},
			},
		})
	}
}

func (oc *ObjectsController) GetServiceRelations(w io.Writer) {
	oc.serviceObjs.rangeServices(func(namespace string, services serviceEntities) {
		for _, svc := range services {
			if len(svc.selector) > 0 {
				pods := oc.podObjs.GetByNamespace(namespace)
				for _, pod := range pods {
					if !matchLabels(svc.selector, pod.Labels) {
						continue
					}
					relationBytes(w, relationMetric{
						Name: relationPodService,
						Labels: []relationLabel{
							{Name: "namespace", Value: namespace},
							{Name: "service", Value: svc.name},
							{Name: "pod", Value: pod.ID.Name},
						},
					})
				}
			}

			for _, addr := range svc.externalIPs {
				relationBytes(w, relationMetric{
					Name: relationK8sAddressService,
					Labels: []relationLabel{
						{Name: "namespace", Value: svc.namespace},
						{Name: "service", Value: svc.name},
						{Name: "address", Value: addr},
					},
				})
			}

			oc.ingressObjs.Range(namespace, func(name string, ingress ingressEntity) {
				for _, s := range ingress.services {
					if s != svc.name {
						continue
					}
					relationBytes(w, relationMetric{
						Name: relationIngressService,
						Labels: []relationLabel{
							{Name: "namespace", Value: svc.namespace},
							{Name: "service", Value: svc.name},
							{Name: "ingress", Value: name},
						},
					})
				}
			})

			switch svc.kind {
			case string(corev1.ServiceTypeExternalName):
				eps, ok := oc.endpointsObjs.getEndpoints(svc.namespace, svc.name)
				if !ok {
					relationBytes(w, relationMetric{
						Name: relationDomainService,
						Labels: []relationLabel{
							{Name: "namespace", Value: svc.namespace},
							{Name: "service", Value: svc.name},
							{Name: "domain", Value: svc.externalName},
						},
					})
				} else {
					for _, addr := range eps.addresses {
						relationBytes(w, relationMetric{
							Name: relationK8sAddressService,
							Labels: []relationLabel{
								{Name: "namespace", Value: svc.namespace},
								{Name: "service", Value: svc.name},
								{Name: "address", Value: addr},
							},
						})
					}
				}

			case string(corev1.ServiceTypeLoadBalancer):
				for _, addr := range svc.loadBalancerIPs {
					relationBytes(w, relationMetric{
						Name: relationK8sAddressService,
						Labels: []relationLabel{
							{Name: "namespace", Value: svc.namespace},
							{Name: "service", Value: svc.name},
							{Name: "address", Value: addr},
						},
					})
				}
			}
		}
	})
}

func (oc *ObjectsController) GetReplicasetRelations(w io.Writer) {
	for _, rs := range oc.replicaSetObjs.GetAll() {
		ownerRef := LookupOnce(rs.ID, oc.replicaSetObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		labels := []relationLabel{
			{Name: "namespace", Value: rs.ID.Namespace},
			{Name: "replicaset", Value: rs.ID.Name},
		}

		switch ownerRef.Kind {
		case kindDeployment:
			labels = append(labels, relationLabel{
				Name:  "deployment",
				Value: ownerRef.Name,
			})
			relationBytes(w, relationMetric{
				Name:   relationDeploymentReplicaset,
				Labels: labels,
			})
		}
	}
}

func (oc *ObjectsController) GetDataSourceRelations(w io.Writer) {
	pods := oc.podObjs.GetAll()
	nodes := oc.nodeObjs.GetAll()

	oc.bkLogConfigObjs.Range(func(e *bkLogConfigEntity) {
		relationBytes(w, relationMetric{
			Name: relationBkLogConfigWithDataSource,
			Labels: []relationLabel{
				{Name: "bk_data_id", Value: fmt.Sprintf("%d", e.Obj.Spec.DataId)},
				{Name: "bklogconfig_namespace", Value: e.Obj.Namespace},
				{Name: "bklogconfig_name", Value: e.Obj.Name},
			},
		})

		switch e.Obj.Spec.LogConfigType {
		case logConfigTypeStd, logConfigTypeContainer:
			for _, pod := range pods {
				if !e.MatchNamespace(pod.ID.Namespace) {
					continue
				}

				if !e.Obj.Spec.AllContainer {
					if !e.MatchLabel(pod.Labels) {
						continue
					}

					if !e.MatchAnnotation(pod.Annotations) {
						continue
					}

					if !e.MatchWorkload(pod.Labels, pod.Annotations, pod.OwnerRefs) {
						continue
					}
				}

				podRelationStatus := false
				for _, container := range pod.Containers {
					if !e.Obj.Spec.AllContainer {
						if !e.MatchContainerName(container) {
							continue
						}
					}
					podRelationStatus = true
				}

				// 只需要上报到 pod 层级就够了
				if podRelationStatus {
					relationBytes(w, relationMetric{
						Name: relationDataSourceWithPod,
						Labels: []relationLabel{
							{Name: "bk_data_id", Value: fmt.Sprintf("%d", e.Obj.Spec.DataId)},
							{Name: "namespace", Value: pod.ID.Namespace},
							{Name: "pod", Value: pod.ID.Name},
						},
					})
				}
			}

		case logConfigTypeNode:
			for _, node := range nodes {
				if !e.MatchLabel(node.GetLabels()) {
					continue
				}

				if !e.MatchAnnotation(node.GetAnnotations()) {
					continue
				}

				relationBytes(w, relationMetric{
					Name: relationDataSourceWithNode,
					Labels: []relationLabel{
						{Name: "bk_data_id", Value: fmt.Sprintf("%d", e.Obj.Spec.DataId)},
						{Name: "node", Value: node.Name},
					},
				})
			}
		}
	})
}

type StatefulSetWorker struct {
	PodIP string
	Index int
}

type PodInfo struct {
	Name      string
	Namespace string
	IP        string
}

func (oc *ObjectsController) AllPods() []PodInfo {
	var pods []PodInfo
	for _, pod := range oc.podObjs.GetAll() {
		pods = append(pods, PodInfo{
			Name:      pod.ID.Name,
			Namespace: pod.ID.Namespace,
			IP:        pod.PodIP,
		})
	}
	return pods
}

func (oc *ObjectsController) GetPods(s string) map[string]StatefulSetWorker {
	regex, err := regexp.Compile(s)
	if err != nil {
		return nil
	}

	// bkm-statefulset-worker-0 => [0]
	parseIndex := func(s string) int {
		parts := strings.Split(s, "-")
		if len(parts) <= 0 {
			return 0
		}
		last := parts[len(parts)-1]
		index, _ := strconv.ParseInt(last, 10, 64)
		return int(index)
	}

	items := make(map[string]StatefulSetWorker)
	for _, pod := range oc.podObjs.GetAll() {
		if regex.MatchString(pod.ID.String()) {
			// 确保 podip 已经获取到
			if pod.PodIP == "" {
				continue
			}
			items[pod.PodIP] = StatefulSetWorker{
				PodIP: pod.PodIP,
				Index: parseIndex(pod.ID.Name),
			}
		}
	}
	return items
}

func (oc *ObjectsController) GetPodRelations(w io.Writer) {
	for _, pod := range oc.podObjs.GetAll() {
		ownerRef := LookupOnce(pod.ID, oc.podObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		relationBytes(w, relationMetric{
			Name: relationNodePod,
			Labels: []relationLabel{
				{Name: "namespace", Value: pod.ID.Namespace},
				{Name: "pod", Value: pod.ID.Name},
				{Name: "node", Value: pod.NodeName},
			},
		})

		// 遍历 containers
		for _, container := range pod.Containers {
			relationBytes(w, relationMetric{
				Name: relationContainerPod,
				Labels: []relationLabel{
					{Name: "namespace", Value: pod.ID.Namespace},
					{Name: "pod", Value: pod.ID.Name},
					{Name: "node", Value: pod.NodeName},
					{Name: "container", Value: container},
				},
			})
		}

		labels := []relationLabel{
			{Name: "namespace", Value: pod.ID.Namespace},
			{Name: "pod", Value: pod.ID.Name},
		}
		switch ownerRef.Kind {
		case kindJob:
			labels = append(labels, relationLabel{
				Name:  "job",
				Value: ownerRef.Name,
			})
			relationBytes(w, relationMetric{
				Name:   relationJobPod,
				Labels: labels,
			})

		case kindReplicaSet:
			labels = append(labels, relationLabel{
				Name:  "replicaset",
				Value: ownerRef.Name,
			})
			relationBytes(w, relationMetric{
				Name:   relationPodReplicaset,
				Labels: labels,
			})

		case kindGameStatefulSet:
			labels = append(labels, relationLabel{
				Name:  "statefulset",
				Value: ownerRef.Name,
			})
			relationBytes(w, relationMetric{
				Name:   relationPodStatefulset,
				Labels: labels,
			})

		case kindDaemonSet:
			labels = append(labels, relationLabel{
				Name:  "daemonset",
				Value: ownerRef.Name,
			})
			relationBytes(w, relationMetric{
				Name:   relationDaemonsetPod,
				Labels: labels,
			})
		}
	}
}

func relationBytes(w io.Writer, metrics ...relationMetric) {
	for _, metric := range metrics {
		w.Write([]byte(metric.Name))
		w.Write([]byte(`{`))

		var n int
		for _, label := range metric.Labels {
			if n > 0 {
				w.Write([]byte(`,`))
			}
			n++
			w.Write([]byte(label.Name))
			w.Write([]byte(`="`))
			w.Write([]byte(label.Value))
			w.Write([]byte(`"`))
		}

		w.Write([]byte("} 1"))
		w.Write([]byte("\n"))
	}
}
