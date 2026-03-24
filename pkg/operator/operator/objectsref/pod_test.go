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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPodMap(t *testing.T) {
	objs := NewPodMap()
	objs.Set(PodObject{
		ID: ObjectID{
			Name:      "obj1",
			Namespace: "ns1",
		},
		NodeName: "node1",
	})
	objs.Set(PodObject{
		ID: ObjectID{
			Name:      "obj2",
			Namespace: "ns1",
		},
		NodeName: "node1",
	})
	assert.Len(t, objs.GetByNamespace("ns1"), 2)
	assert.Len(t, objs.GetByNodeName("node1"), 2)
	assert.Len(t, objs.GetByNodeName("node2"), 0)

	objs.Del(ObjectID{
		Name:      "obj1",
		Namespace: "ns1",
	})
	assert.Len(t, objs.GetByNamespace("ns1"), 1)
	assert.Len(t, objs.GetByNodeName("node1"), 1)
}
