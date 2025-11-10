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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/promfmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/utils"
)

const (
	relationContainerInfoPath = "monitoring.bk.tencent.com/relation/info/container/"
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
	relationAppVersionWithContainer   = "app_version_with_container_relation"

	relationContainerInfo = "container_info_relation"
)

func (oc *ObjectsController) WriteAppVersionWithContainerRelation(w io.Writer) {
	for _, pod := range oc.podObjs.GetAll() {
		var customLabels []promfmt.Label
		for k, v := range pod.Annotations {
			if strings.HasPrefix(k, relationContainerInfoPath) {
				name := strings.TrimPrefix(k, relationContainerInfoPath)
				if name == "" || v == "" {
					continue
				}
				customLabels = append(customLabels, promfmt.Label{
					Name:  name,
					Value: v,
				})
			}
		}

		for _, container := range pod.Containers {
			if container.ImageTag == "" || container.Name == "" {
				continue
			}

			labels := append([]promfmt.Label{
				{Name: "pod", Value: pod.ID.Name},
				{Name: "namespace", Value: pod.ID.Namespace},
				{Name: "container", Value: container.Name},
				{Name: "app_name", Value: container.ImageName},
				{Name: "version", Value: container.ImageTag},
			}, customLabels...)

			promfmt.FmtBytes(w, promfmt.Metric{
				Name:   relationContainerInfo,
				Labels: labels,
			})

			promfmt.FmtBytes(w, promfmt.Metric{
				Name: relationAppVersionWithContainer,
				Labels: []promfmt.Label{
					{Name: "pod", Value: pod.ID.Name},
					{Name: "namespace", Value: pod.ID.Namespace},
					{Name: "container", Value: container.Name},
					{Name: "app_name", Value: container.ImageName},
					{Name: "version", Value: container.ImageTag},
				},
			})
		}
	}
}

func (oc *ObjectsController) WriteNodeRelations(w io.Writer) {
	for node, ip := range oc.nodeObjs.Addrs() {
		promfmt.FmtBytes(w, promfmt.Metric{
			Name: relationNodeSystem,
			Labels: []promfmt.Label{
				{Name: "node", Value: node},
				{Name: "bk_target_ip", Value: ip},
			},
		})
	}
}

func (oc *ObjectsController) WriteServiceRelations(w io.Writer) {
	oc.serviceObjs.Range(func(namespace string, services serviceEntities) {
		pods := oc.podObjs.GetByNamespace(namespace)
		for _, svc := range services {
			if len(svc.selector) > 0 {
				for _, pod := range pods {
					if !utils.MatchSubLabels(svc.selector, pod.Labels) {
						continue
					}
					promfmt.FmtBytes(w, promfmt.Metric{
						Name: relationPodService,
						Labels: []promfmt.Label{
							{Name: "namespace", Value: namespace},
							{Name: "service", Value: svc.name},
							{Name: "pod", Value: pod.ID.Name},
						},
					})
				}
			}

			for _, addr := range svc.externalIPs {
				promfmt.FmtBytes(w, promfmt.Metric{
					Name: relationK8sAddressService,
					Labels: []promfmt.Label{
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
					promfmt.FmtBytes(w, promfmt.Metric{
						Name: relationIngressService,
						Labels: []promfmt.Label{
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
					promfmt.FmtBytes(w, promfmt.Metric{
						Name: relationDomainService,
						Labels: []promfmt.Label{
							{Name: "namespace", Value: svc.namespace},
							{Name: "service", Value: svc.name},
							{Name: "domain", Value: svc.externalName},
						},
					})
				} else {
					for _, addr := range eps.addresses {
						promfmt.FmtBytes(w, promfmt.Metric{
							Name: relationK8sAddressService,
							Labels: []promfmt.Label{
								{Name: "namespace", Value: svc.namespace},
								{Name: "service", Value: svc.name},
								{Name: "address", Value: addr},
							},
						})
					}
				}

			case string(corev1.ServiceTypeLoadBalancer):
				for _, addr := range svc.loadBalancerIPs {
					promfmt.FmtBytes(w, promfmt.Metric{
						Name: relationK8sAddressService,
						Labels: []promfmt.Label{
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

func (oc *ObjectsController) WriteReplicasetRelations(w io.Writer) {
	for _, rs := range oc.replicaSetObjs.GetAll() {
		ownerRef := LookupOnce(rs.ID, oc.replicaSetObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		labels := []promfmt.Label{
			{Name: "namespace", Value: rs.ID.Namespace},
			{Name: "replicaset", Value: rs.ID.Name},
		}

		switch ownerRef.Kind {
		case kindDeployment:
			labels = append(labels, promfmt.Label{
				Name:  "deployment",
				Value: ownerRef.Name,
			})
			promfmt.FmtBytes(w, promfmt.Metric{
				Name:   relationDeploymentReplicaset,
				Labels: labels,
			})
		}
	}
}

func (oc *ObjectsController) WriteDataSourceRelations(w io.Writer) {
	pods := oc.podObjs.GetAll()
	nodes := oc.nodeObjs.GetAll()

	oc.bkLogConfigObjs.Range(func(e *bkLogConfigEntity) {
		promfmt.FmtBytes(w, promfmt.Metric{
			Name: relationBkLogConfigWithDataSource,
			Labels: []promfmt.Label{
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
						if !e.MatchContainerName(container.Name) {
							continue
						}
					}
					podRelationStatus = true
				}

				// 只需要上报到 pod 层级就够了
				if podRelationStatus {
					promfmt.FmtBytes(w, promfmt.Metric{
						Name: relationDataSourceWithPod,
						Labels: []promfmt.Label{
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

				promfmt.FmtBytes(w, promfmt.Metric{
					Name: relationDataSourceWithNode,
					Labels: []promfmt.Label{
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

func (oc *ObjectsController) FetchPodEvents(rv int) ([]PodEvent, int) {
	return oc.podObjs.FetchEvents(rv)
}

func (oc *ObjectsController) GetPods(s string) map[string]StatefulSetWorker {
	regex, err := regexp.Compile(s)
	if err != nil {
		return nil
	}

	// bkm-statefulset-worker-0 => [0]
	parseIndex := func(s string) int {
		parts := strings.Split(s, "-")
		if len(parts) == 0 {
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

func (oc *ObjectsController) WritePodRelations(w io.Writer) {
	for _, pod := range oc.podObjs.GetAll() {
		ownerRef := LookupOnce(pod.ID, oc.podObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		promfmt.FmtBytes(w, promfmt.Metric{
			Name: relationNodePod,
			Labels: []promfmt.Label{
				{Name: "namespace", Value: pod.ID.Namespace},
				{Name: "pod", Value: pod.ID.Name},
				{Name: "node", Value: pod.NodeName},
			},
		})

		// 遍历 containers
		for _, container := range pod.Containers {
			promfmt.FmtBytes(w, promfmt.Metric{
				Name: relationContainerPod,
				Labels: []promfmt.Label{
					{Name: "namespace", Value: pod.ID.Namespace},
					{Name: "pod", Value: pod.ID.Name},
					{Name: "node", Value: pod.NodeName},
					{Name: "container", Value: container.Name},
				},
			})
		}

		labels := []promfmt.Label{
			{Name: "namespace", Value: pod.ID.Namespace},
			{Name: "pod", Value: pod.ID.Name},
		}
		switch ownerRef.Kind {
		case kindJob:
			labels = append(labels, promfmt.Label{
				Name:  "job",
				Value: ownerRef.Name,
			})
			promfmt.FmtBytes(w, promfmt.Metric{
				Name:   relationJobPod,
				Labels: labels,
			})

		case kindReplicaSet:
			labels = append(labels, promfmt.Label{
				Name:  "replicaset",
				Value: ownerRef.Name,
			})
			promfmt.FmtBytes(w, promfmt.Metric{
				Name:   relationPodReplicaset,
				Labels: labels,
			})

		case kindGameStatefulSet:
			labels = append(labels, promfmt.Label{
				Name:  "statefulset",
				Value: ownerRef.Name,
			})
			promfmt.FmtBytes(w, promfmt.Metric{
				Name:   relationPodStatefulset,
				Labels: labels,
			})

		case kindDaemonSet:
			labels = append(labels, promfmt.Label{
				Name:  "daemonset",
				Value: ownerRef.Name,
			})
			promfmt.FmtBytes(w, promfmt.Metric{
				Name:   relationDaemonsetPod,
				Labels: labels,
			})
		}
	}
}
