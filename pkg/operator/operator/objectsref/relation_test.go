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
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	loggingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/logging/v1alpha1"
)

func TestWritePodRelations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	podObject := PodObject{
		ID: ObjectID{
			Name:      "test-pod-1",
			Namespace: "test-ns-1",
		},
		NodeName: "test-node-1",
		Containers: []ContainerKey{
			{Name: "test-container-1"},
			{Name: "test-container-2"},
		},
	}

	objectsController := &ObjectsController{
		ctx:    ctx,
		cancel: cancel,
		podObjs: &PodMap{
			objs: map[string]PodObject{
				podObject.ID.String(): podObject,
			},
		},
	}

	buf := &bytes.Buffer{}
	objectsController.WritePodRelations(buf)

	expected := `node_with_pod_relation{namespace="test-ns-1",pod="test-pod-1",node="test-node-1"} 1
container_with_pod_relation{namespace="test-ns-1",pod="test-pod-1",node="test-node-1",container="test-container-1"} 1
container_with_pod_relation{namespace="test-ns-1",pod="test-pod-1",node="test-node-1",container="test-container-2"} 1
`
	assert.Equal(t, expected, buf.String())
}

func TestWriteDataSourceRelations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pods := []PodObject{
		{
			ID: ObjectID{
				Name:      "unify-query-01",
				Namespace: "blueking",
			},
			Labels: map[string]string{
				"app.kubernetes.io/instance": "bkmonitor",
				"app.kubernetes.io/name":     "unify-query",
			},
			NodeName:   "127-0-0-1",
			Containers: []ContainerKey{{Name: "unify-query"}},
		},
		{
			ID: ObjectID{
				Name:      "unify-query-02",
				Namespace: "blueking",
			},
			Labels: map[string]string{
				"app.kubernetes.io/instance": "bkmonitor",
				"app.kubernetes.io/name":     "unify-query",
			},
			NodeName:   "127-0-0-2",
			Containers: []ContainerKey{{Name: "unify-query"}},
		},
		{
			ID: ObjectID{
				Name:      "unify-query-03",
				Namespace: "default",
			},
			Labels: map[string]string{
				"app.kubernetes.io/instance": "bkmonitor",
				"app.kubernetes.io/name":     "unify-query",
			},
			NodeName:   "127-0-0-3",
			Containers: []ContainerKey{{Name: "unify-query"}},
		},
	}
	podsMap := make(map[string]PodObject, len(pods))
	for _, p := range pods {
		podsMap[p.ID.String()] = p
	}

	nodes := []*corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "127-0-0-1",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "bkmonitor",
					"app.kubernetes.io/name":     "unify-query",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "127-0-0-2",
				Labels: map[string]string{
					"app.kubernetes.io/instance": "bkmonitor",
					"app.kubernetes.io/name":     "unify-query",
				},
			},
		},
	}
	nodesMap := make(map[string]*corev1.Node, len(nodes))
	for _, n := range nodes {
		nodesMap[n.Name] = n
	}

	testCases := map[string]struct {
		bkLogConfig string
		expected    []string
	}{
		"std_log_config_1": {
			bkLogConfig: `{
    "apiVersion": "bk.tencent.com/v1alpha1",
    "kind": "BkLogConfig",
    "metadata": {
        "annotations": {
            "meta.helm.sh/release-name": "bkmonitor",
            "meta.helm.sh/release-namespace": "default"
        },
        "generation": 5,
        "labels": {
            "app.kubernetes.io/instance": "bkmonitor",
            "app.kubernetes.io/managed-by": "Helm",
            "app.kubernetes.io/name": "unify-query",
            "helm.sh/chart": "unify-query-0.1.0"
        },
        "name": "bkmonitor-unify-query-container-log",
        "namespace": "blueking"
    },
    "spec": {
        "dataId": 100001,
        "encoding": "utf-8",
        "labelSelector": {
            "matchLabels": {
                "app.kubernetes.io/instance": "bkmonitor",
                "app.kubernetes.io/name": "unify-query"
            }
        },
        "logConfigType": "std_log_config",
        "namespace": "blueking"
    }
}`,
			expected: []string{
				`bklogconfig_with_datasource_relation{bk_data_id="100001",bklogconfig_namespace="blueking",bklogconfig_name="bkmonitor-unify-query-container-log"} 1`,
				`datasource_with_pod_relation{bk_data_id="100001",namespace="blueking",pod="unify-query-01"} 1`,
				`datasource_with_pod_relation{bk_data_id="100001",namespace="blueking",pod="unify-query-02"} 1`,
			},
		},
		"std_log_config_2": {
			bkLogConfig: `{
    "apiVersion": "bk.tencent.com/v1alpha1",
    "kind": "BkLogConfig",
    "metadata": {
        "annotations": {
            "meta.helm.sh/release-name": "bkmonitor",
            "meta.helm.sh/release-namespace": "default"
        },
        "generation": 5,
        "labels": {
            "app.kubernetes.io/instance": "bkmonitor",
            "app.kubernetes.io/managed-by": "Helm",
            "app.kubernetes.io/name": "unify-query",
            "helm.sh/chart": "unify-query-0.1.0"
        },
        "name": "bkmonitor-unify-query-container-log",
        "namespace": "blueking"
    },
    "spec": {
        "dataId": 100001,
        "encoding": "utf-8",
        "labelSelector": {
            "matchLabels": {
                "app.kubernetes.io/instance": "bkmonitor",
                "app.kubernetes.io/name": "unify-query"
            }
        },
        "logConfigType": "container_log_config",
        "namespace": "default"
    }
}`,
			expected: []string{
				`bklogconfig_with_datasource_relation{bk_data_id="100001",bklogconfig_namespace="blueking",bklogconfig_name="bkmonitor-unify-query-container-log"} 1`,
				`datasource_with_pod_relation{bk_data_id="100001",namespace="default",pod="unify-query-03"} 1`,
			},
		},
		"std_log_config_3": {
			bkLogConfig: `{
    "apiVersion": "bk.tencent.com/v1alpha1",
    "kind": "BkLogConfig",
    "metadata": {
        "annotations": {
            "meta.helm.sh/release-name": "bkmonitor",
            "meta.helm.sh/release-namespace": "default"
        },
        "generation": 5,
        "labels": {
            "app.kubernetes.io/instance": "bkmonitor",
            "app.kubernetes.io/managed-by": "Helm",
            "app.kubernetes.io/name": "unify-query",
            "helm.sh/chart": "unify-query-0.1.0"
        },
        "name": "bkmonitor-unify-query-container-log",
        "namespace": "blueking"
    },
    "spec": {
        "dataId": 100001,
        "encoding": "utf-8",
        "labelSelector": {
            "matchLabels": {
                "app.kubernetes.io/instance": "bkmonitor",
                "app.kubernetes.io/name": "unify-query-1"
            }
        },
        "logConfigType": "container_log_config",
		"allContainer": true,
        "namespaceSelector": {
			"any": true
        }
    }
}`,
			expected: []string{
				`bklogconfig_with_datasource_relation{bk_data_id="100001",bklogconfig_namespace="blueking",bklogconfig_name="bkmonitor-unify-query-container-log"} 1`,
				`datasource_with_pod_relation{bk_data_id="100001",namespace="blueking",pod="unify-query-01"} 1`,
				`datasource_with_pod_relation{bk_data_id="100001",namespace="blueking",pod="unify-query-02"} 1`,
				`datasource_with_pod_relation{bk_data_id="100001",namespace="default",pod="unify-query-03"} 1`,
			},
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			objectsController := &ObjectsController{
				ctx:    ctx,
				cancel: cancel,
				nodeObjs: &NodeMap{
					nodes: nodesMap,
				},
				podObjs: &PodMap{
					objs: podsMap,
				},
			}

			var bkLogConfig *loggingv1alpha1.BkLogConfig
			err := json.Unmarshal([]byte(c.bkLogConfig), &bkLogConfig)
			assert.NoError(t, err)

			objectsController.bkLogConfigObjs = &BkLogConfigMap{
				entitiesMap: map[string]*bkLogConfigEntity{
					name: newBkLogConfigEntity(bkLogConfig),
				},
			}

			buf := &bytes.Buffer{}
			objectsController.WriteDataSourceRelations(buf)

			for _, s := range c.expected {
				assert.Contains(t, buf.String(), s)
			}
		})
	}
}

func TestWriteContainerInfoRelation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	podObject := PodObject{
		ID: ObjectID{
			Name:      "test-pod-1",
			Namespace: "test-ns-1",
		},
		NodeName: "test-node-1",
		Annotations: map[string]string{
			"monitoring.bk.tencent.com/relation/info/container/environment": "paasv3",
			"monitoring.bk.tencent.com/relation/info/container/region":      "guangzhou",
		},
		Containers: []ContainerKey{
			{Name: "test-container-1", ImageTag: "1.0.0", ImageName: "test-image"},
			{Name: "test-container-2", ImageTag: "2.0.0", ImageName: "test-image"},
		},
	}

	objectsController := &ObjectsController{
		ctx:    ctx,
		cancel: cancel,
		podObjs: &PodMap{
			objs: map[string]PodObject{
				podObject.ID.String(): podObject,
			},
		},
	}

	buf := &bytes.Buffer{}
	objectsController.WriteAppVersionWithContainerRelation(buf)

	expected := []string{
		`app_version_with_container_relation{pod="test-pod-1",namespace="test-ns-1",container="test-container-1",app_name="test-image",version="1.0.0"} 1`,
		`app_version_with_container_relation{pod="test-pod-1",namespace="test-ns-1",container="test-container-2",app_name="test-image",version="2.0.0"} 1`,
	}
	for _, s := range expected {
		assert.Contains(t, buf.String(), s)
	}
}
