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
	"testing"

	"github.com/stretchr/testify/assert"
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
