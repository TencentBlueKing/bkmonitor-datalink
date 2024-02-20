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
	"bytes"

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

	relationPodService     = "pod_with_service_relation"
	relationAddressService = "address_with_service_relation"
	relationDomainService  = "domain_with_service_relation"
	relationIngressService = "ingress_with_service_relation"
)

type RelationMetric struct {
	Name   string
	Labels []RelationLabel
}

type RelationLabel struct {
	Name  string
	Value string
}

func (oc *ObjectsController) GetNodeRelations() []RelationMetric {
	var metrics []RelationMetric
	for node, ip := range oc.nodeObjs.Addrs() {
		metrics = append(metrics, RelationMetric{
			Name: relationNodeSystem,
			Labels: []RelationLabel{
				{Name: "node", Value: node},
				{Name: "bk_target_ip", Value: ip},
			},
		})
	}
	return metrics
}

func (oc *ObjectsController) GetServieRelations() []RelationMetric {
	var metrics []RelationMetric
	oc.serviceObjs.rangeServices(func(namespace string, services serviceEntities) {
		for _, svc := range services {
			if len(svc.selector) > 0 {
				pods := oc.podObjs.GetByNamespace(namespace)
				for _, pod := range pods {
					if !matchLabels(svc.selector, pod.Labels) {
						continue
					}
					metrics = append(metrics, RelationMetric{
						Name: relationPodService,
						Labels: []RelationLabel{
							{Name: "namespace", Value: namespace},
							{Name: "service", Value: svc.name},
							{Name: "pod", Value: pod.ID.Name},
						},
					})
				}
			}

			for _, addr := range svc.externalIPs {
				metrics = append(metrics, RelationMetric{
					Name: relationAddressService,
					Labels: []RelationLabel{
						{Name: "namespace", Value: svc.namespace},
						{Name: "service", Value: svc.name},
						{Name: "address", Value: addr},
					},
				})
			}

			oc.ingressObjs.rangeIngress(namespace, func(name string, ingress ingressEntity) {
				for _, s := range ingress.services {
					if s != svc.name {
						continue
					}
					metrics = append(metrics, RelationMetric{
						Name: relationIngressService,
						Labels: []RelationLabel{
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
					metrics = append(metrics, RelationMetric{
						Name: relationDomainService,
						Labels: []RelationLabel{
							{Name: "namespace", Value: svc.namespace},
							{Name: "service", Value: svc.name},
							{Name: "domain", Value: svc.externalName},
						},
					})
				} else {
					for _, addr := range eps.addresses {
						metrics = append(metrics, RelationMetric{
							Name: relationAddressService,
							Labels: []RelationLabel{
								{Name: "namespace", Value: svc.namespace},
								{Name: "service", Value: svc.name},
								{Name: "address", Value: addr},
							},
						})
					}
				}

			case string(corev1.ServiceTypeLoadBalancer):
				for _, addr := range svc.loadBalancerIPs {
					metrics = append(metrics, RelationMetric{
						Name: relationAddressService,
						Labels: []RelationLabel{
							{Name: "namespace", Value: svc.namespace},
							{Name: "service", Value: svc.name},
							{Name: "address", Value: addr},
						},
					})
				}
			}
		}
	})

	return metrics
}

func (oc *ObjectsController) GetReplicasetRelations() []RelationMetric {
	var metrics []RelationMetric
	for _, rs := range oc.replicaSetObjs.GetAll() {
		ownerRef := LookupOnce(rs.ID, oc.replicaSetObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		labels := []RelationLabel{
			{Name: "namespace", Value: rs.ID.Namespace},
			{Name: "replicaset", Value: rs.ID.Name},
		}

		switch ownerRef.Kind {
		case kindDeployment:
			labels = append(labels, RelationLabel{
				Name:  "deployment",
				Value: ownerRef.Name,
			})
			metrics = append(metrics, RelationMetric{
				Name:   relationDeploymentReplicaset,
				Labels: labels,
			})
		}
	}
	return metrics
}

func (oc *ObjectsController) GetPodRelations() []RelationMetric {
	var metrics []RelationMetric
	for _, pod := range oc.podObjs.GetAll() {
		ownerRef := LookupOnce(pod.ID, oc.podObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		metrics = append(metrics, RelationMetric{
			Name: relationNodePod,
			Labels: []RelationLabel{
				{Name: "namespace", Value: pod.ID.Namespace},
				{Name: "pod", Value: pod.ID.Name},
				{Name: "node", Value: pod.NodeName},
			},
		})

		labels := []RelationLabel{
			{Name: "namespace", Value: pod.ID.Namespace},
			{Name: "pod", Value: pod.ID.Name},
		}
		switch ownerRef.Kind {
		case kindJob:
			labels = append(labels, RelationLabel{
				Name:  "job",
				Value: ownerRef.Name,
			})
			metrics = append(metrics, RelationMetric{
				Name:   relationJobPod,
				Labels: labels,
			})

		case kindReplicaSet:
			labels = append(labels, RelationLabel{
				Name:  "replicaset",
				Value: ownerRef.Name,
			})
			metrics = append(metrics, RelationMetric{
				Name:   relationPodReplicaset,
				Labels: labels,
			})

		case kindGameStatefulSet:
			labels = append(labels, RelationLabel{
				Name:  "statefulset",
				Value: ownerRef.Name,
			})
			metrics = append(metrics, RelationMetric{
				Name:   relationPodStatefulset,
				Labels: labels,
			})

		case kindDaemonSet:
			labels = append(labels, RelationLabel{
				Name:  "daemonset",
				Value: ownerRef.Name,
			})
			metrics = append(metrics, RelationMetric{
				Name:   relationDaemonsetPod,
				Labels: labels,
			})
		}
	}
	return metrics
}

func RelationToPromFormat(metrics []RelationMetric) []byte {
	var lines []byte
	for _, metric := range metrics {
		var buf bytes.Buffer
		buf.WriteString(metric.Name)
		buf.WriteString(`{`)

		var n int
		for _, label := range metric.Labels {
			if n > 0 {
				buf.WriteString(`,`)
			}
			n++
			buf.WriteString(label.Name)
			buf.WriteString(`="`)
			buf.WriteString(label.Value)
			buf.WriteString(`"`)
		}

		buf.WriteString("} 1")
		buf.WriteString("\n")
		lines = append(lines, buf.Bytes()...)
	}
	return lines
}
