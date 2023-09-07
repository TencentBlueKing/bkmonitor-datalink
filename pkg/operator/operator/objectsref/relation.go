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
)

// NOTE: 实验性功能（Experimental）后续可能会持续迭代 或者删除

const (
	relationNodeSystem           = "node_with_system_relation"
	relationNodePod              = "node_with_pod_relation"
	relationJobPod               = "job_with_pod_relation"
	relationPodReplicaset        = "pod_with_replicaset_relation"
	relationPodStatefulset       = "pod_with_statefulset_relation"
	relationDaemonsetPod         = "daemonset_with_pod_relation"
	relationDeploymentReplicaset = "deployment_with_replicaset_relation"

	// TODO(mando): 待实现
	relationEndpointPod            = "endpoint_with_pod_relation"
	relationEndpointService        = "endpoint_with_service_relation"
	relationIngressServiceRelation = "ingress_with_service_relation"
)

type RelationMetric struct {
	Name      string
	Dimension map[string]string
}

func (oc *ObjectsController) GetNodeRelation() []RelationMetric {
	var metrics []RelationMetric
	for node, ip := range oc.nodeObjs.Addrs() {
		metrics = append(metrics, RelationMetric{
			Name: relationNodeSystem,
			Dimension: map[string]string{
				"node":         node,
				"bk_target_ip": ip,
			},
		})
	}
	return metrics
}

func (oc *ObjectsController) GetReplicasetRelation() []RelationMetric {
	var metrics []RelationMetric
	for _, rs := range oc.replicaSetObjs.GetAll() {
		ownerRef := LookupOnce(rs.ID, oc.replicaSetObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		dims := map[string]string{
			"replicaset": rs.ID.Name,
			"namespace":  rs.ID.Namespace,
		}

		switch ownerRef.Kind {
		case kindDeployment:
			dims["deployment"] = ownerRef.Name
			metrics = append(metrics, RelationMetric{
				Name:      relationDeploymentReplicaset,
				Dimension: dims,
			})
		}
	}
	return metrics
}

func (oc *ObjectsController) GetPodRelation() []RelationMetric {
	var metrics []RelationMetric
	for _, pod := range oc.podObjs.GetAll() {
		ownerRef := LookupOnce(pod.ID, oc.podObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		metrics = append(metrics, RelationMetric{
			Name: relationNodePod,
			Dimension: map[string]string{
				"node":      pod.NodeName,
				"pod":       pod.ID.Name,
				"namespace": pod.ID.Namespace,
			},
		})

		dims := map[string]string{
			"pod":       pod.ID.Name,
			"namespace": pod.ID.Namespace,
		}
		switch ownerRef.Kind {
		case kindJob:
			dims["job"] = ownerRef.Name
			metrics = append(metrics, RelationMetric{
				Name:      relationJobPod,
				Dimension: dims,
			})

		case kindReplicaSet:
			dims["replicaset"] = ownerRef.Name
			metrics = append(metrics, RelationMetric{
				Name:      relationPodReplicaset,
				Dimension: dims,
			})

		case kindGameStatefulSet:
			dims["statefulset"] = ownerRef.Name
			metrics = append(metrics, RelationMetric{
				Name:      relationPodStatefulset,
				Dimension: dims,
			})

		case kindDaemonSet:
			dims["daemonset"] = ownerRef.Name
			metrics = append(metrics, RelationMetric{
				Name:      relationDaemonsetPod,
				Dimension: dims,
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
		for k, v := range metric.Dimension {
			if n > 0 {
				buf.WriteString(`,`)
			}
			n++
			buf.WriteString(k)
			buf.WriteString(`="`)
			buf.WriteString(v)
			buf.WriteString(`"`)
		}

		buf.WriteString("} 1")
		buf.WriteString("\n")
		lines = append(lines, buf.Bytes()...)
	}
	return lines
}
