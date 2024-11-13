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

func TestMetricsToPrometheusFormat(t *testing.T) {
	t.Run("Labels/Count=2", func(t *testing.T) {
		rows := []relationMetric{
			{
				Name: "usage",
				Labels: []relationLabel{
					{Name: "cpu", Value: "1"},
					{Name: "biz", Value: "0"},
				},
			},
			{
				Name: "usage",
				Labels: []relationLabel{
					{Name: "cpu", Value: "2"},
					{Name: "biz", Value: "0"},
				},
			},
		}

		buf := &bytes.Buffer{}
		relationBytes(buf, rows...)

		expected := `usage{cpu="1",biz="0"} 1
usage{cpu="2",biz="0"} 1
`
		assert.Equal(t, expected, buf.String())
	})

	t.Run("Labels/Count=1", func(t *testing.T) {
		rows := []relationMetric{
			{
				Name: "usage",
				Labels: []relationLabel{
					{Name: "cpu", Value: "1"},
				},
			},
			{
				Name: "usage",
				Labels: []relationLabel{
					{Name: "cpu", Value: "2"},
				},
			},
		}

		buf := &bytes.Buffer{}
		relationBytes(buf, rows...)

		expected := `usage{cpu="1"} 1
usage{cpu="2"} 1
`
		assert.Equal(t, expected, buf.String())
	})
}

func TestGetPodRelations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	containers := []string{"test-container-1", "test-container-2"}
	pod := "test-pod-1"
	namespace := "test-ns-1"
	node := "test-node-1"

	podObject := Object{
		ID: ObjectID{
			Name:      pod,
			Namespace: namespace,
		},
		NodeName:   node,
		Containers: containers,
	}

	objectsController := &ObjectsController{
		ctx:    ctx,
		cancel: cancel,
		podObjs: &Objects{
			kind: kindPod,
			objs: map[string]Object{
				podObject.ID.String(): podObject,
			},
		},
	}

	buf := &bytes.Buffer{}
	objectsController.GetPodRelations(buf)

	expected := `node_with_pod_relation{namespace="test-ns-1",pod="test-pod-1",node="test-node-1"} 1
container_with_pod_relation{namespace="test-ns-1",pod="test-pod-1",node="test-node-1",container="test-container-1"} 1
container_with_pod_relation{namespace="test-ns-1",pod="test-pod-1",node="test-node-1",container="test-container-2"} 1
`
	assert.Equal(t, expected, buf.String())
}

func TestObjectsController_GetDataSourceRelations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pods := []Object{
		{
			ID: ObjectID{
				Name:      "unify-query-01",
				Namespace: "blueking",
			},
			Labels: map[string]string{
				"app.kubernetes.io/instance": "bkmonitor",
				"app.kubernetes.io/name":     "unify-query",
			},
			NodeName: "127-0-0-1",
			Containers: []string{
				"unify-query",
			},
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
			NodeName: "127-0-0-2",
			Containers: []string{
				"unify-query",
			},
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
			NodeName: "127-0-0-3",
			Containers: []string{
				"unify-query",
			},
		},
	}
	podsMap := make(map[string]Object, len(pods))
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

		expected string
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
			expected: `bk_log_config_with_data_source_relation{bk_data_id="100001",bk_log_config_namespace="blueking",bk_log_config_name="bkmonitor-unify-query-container-log"} 1
data_source_with_pod_relation{bk_data_id="100001",namespace="blueking",pod="unify-query-01"} 1
data_source_with_pod_relation{bk_data_id="100001",namespace="blueking",pod="unify-query-02"} 1
`,
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
			expected: `bk_log_config_with_data_source_relation{bk_data_id="100001",bk_log_config_namespace="blueking",bk_log_config_name="bkmonitor-unify-query-container-log"} 1
data_source_with_pod_relation{bk_data_id="100001",namespace="default",pod="unify-query-03"} 1
`,
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
			expected: `bk_log_config_with_data_source_relation{bk_data_id="100001",bk_log_config_namespace="blueking",bk_log_config_name="bkmonitor-unify-query-container-log"} 1
data_source_with_pod_relation{bk_data_id="100001",namespace="blueking",pod="unify-query-01"} 1
data_source_with_pod_relation{bk_data_id="100001",namespace="blueking",pod="unify-query-02"} 1
data_source_with_pod_relation{bk_data_id="100001",namespace="default",pod="unify-query-03"} 1
`,
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
				podObjs: &Objects{
					kind: kindPod,
					objs: podsMap,
				},
			}

			var bkLogConfig *loggingv1alpha1.BkLogConfig
			err := json.Unmarshal([]byte(c.bkLogConfig), &bkLogConfig)
			if assert.NoError(t, err) {
				objectsController.bkLogConfigObjs = &BkLogConfigMap{
					entitiesMap: map[string]*bkLogConfigEntity{
						name: {
							Obj: bkLogConfig,
						},
					},
				}

				buf := &bytes.Buffer{}
				objectsController.GetDataSourceRelations(buf)

				assert.Equal(t, c.expected, buf.String())
			}
		})
	}

}
