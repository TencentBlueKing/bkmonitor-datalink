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

var (
	podObjs1        = NewPodMap()
	replicaSetObjs1 = NewObjects(kindReplicaSet)
	replicaSetObjs2 = NewObjects(kindReplicaSet)
	deploymentObjs1 = NewObjects(kindDeployment)
	deploymentObjs2 = NewObjects(kindDeployment)
	noneExistent1   = NewObjects(kindNonExistent)
)

const (
	namespaceDefault  = "default"
	namespaceOther    = "other"
	nodeName1         = "node-10-1-2-3"
	nodeName2         = "node-10-1-2-4"
	podName1          = "bkmonitor-operator-stack-operator-deployment-66847fc466-jzvlr"
	podName2          = "bkmonitor-operator-stack-operator-deployment-66847fc466-jzvol"
	replicaSetName1   = "bkmonitor-operator-stack-operator-deployment-66847fc466"
	deploymentName1   = "bkmonitor-operator-stack-operator-deployment"
	noneExistentName1 = "noneExistent-1"

	kindNonExistent = "NoneExistent"
)

func init() {
	podObjs1.Set(PodObject{
		ID: ObjectID{
			Name:      podName1,
			Namespace: namespaceDefault,
		},
		OwnerRefs: []OwnerRef{
			{
				Kind: kindReplicaSet,
				Name: replicaSetName1,
			},
		},
		NodeName: nodeName1,
	})

	podObjs1.Set(PodObject{
		ID: ObjectID{
			Name:      podName1,
			Namespace: namespaceOther,
		},
		OwnerRefs: []OwnerRef{
			{
				Kind: kindReplicaSet,
				Name: replicaSetName1,
			},
		},
		NodeName: nodeName1,
	})

	podObjs1.Set(PodObject{
		ID: ObjectID{
			Name:      podName2,
			Namespace: namespaceDefault,
		},
		OwnerRefs: []OwnerRef{
			{
				Kind: kindReplicaSet,
				Name: replicaSetName1,
			},
		},
		NodeName: nodeName2,
	})

	replicaSetObjs1.Set(Object{
		ID: ObjectID{
			Name:      replicaSetName1,
			Namespace: namespaceDefault,
		},
		OwnerRefs: []OwnerRef{
			{
				Kind: kindDeployment,
				Name: deploymentName1,
			},
		},
	})

	replicaSetObjs2.Set(Object{
		ID: ObjectID{
			Namespace: namespaceDefault,
			Name:      replicaSetName1,
		},
	})

	deploymentObjs1.Set(Object{
		ID: ObjectID{
			Namespace: namespaceDefault,
			Name:      deploymentName1,
		},
	})

	deploymentObjs2.Set(Object{
		ID: ObjectID{
			Namespace: namespaceDefault,
			Name:      deploymentName1,
		},
		OwnerRefs: []OwnerRef{
			{
				Kind: kindNonExistent,
				Name: noneExistentName1,
			},
		},
	})

	noneExistent1.Set(Object{
		ID: ObjectID{
			Namespace: namespaceDefault,
			Name:      noneExistentName1,
		},
	})
}

func TestLookup(t *testing.T) {
	cases := []struct {
		PodName   string
		PodObj    *PodMap
		Namespace string
		Refs      map[string]*Objects
		Excepted  *OwnerRef
	}{
		{
			PodName:   podName1,
			PodObj:    podObjs1,
			Namespace: namespaceDefault,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs1,
				kindDeployment: deploymentObjs1,
			},
			Excepted: &OwnerRef{Kind: kindDeployment, Name: deploymentName1},
		},
		{
			PodName:   podName1,
			PodObj:    podObjs1,
			Namespace: namespaceOther,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs1,
				kindDeployment: deploymentObjs1,
			},
			Excepted: &OwnerRef{Kind: kindPod, Name: podName1},
		},
		{
			PodName:   podName1 + "/",
			PodObj:    podObjs1,
			Namespace: namespaceDefault,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs1,
				kindDeployment: deploymentObjs1,
			},
		},
		{
			PodName:   podName1,
			PodObj:    podObjs1,
			Namespace: namespaceDefault,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs1,
			},
			Excepted: &OwnerRef{Kind: kindReplicaSet, Name: replicaSetName1},
		},
		{
			PodName:   podName2,
			PodObj:    podObjs1,
			Namespace: namespaceDefault,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs1,
				kindDeployment: deploymentObjs1,
			},
			Excepted: &OwnerRef{Kind: kindDeployment, Name: deploymentName1},
		},
		{
			PodName:   podName2,
			PodObj:    podObjs1,
			Namespace: namespaceDefault,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs1,
			},
			Excepted: &OwnerRef{Kind: kindReplicaSet, Name: replicaSetName1},
		},
		{
			PodName:   podName1,
			PodObj:    podObjs1,
			Namespace: namespaceDefault,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs2,
			},
			Excepted: &OwnerRef{Kind: kindReplicaSet, Name: replicaSetName1},
		},

		{
			PodName:   podName1,
			PodObj:    podObjs1,
			Namespace: namespaceDefault,
			Refs: map[string]*Objects{
				kindReplicaSet:  replicaSetObjs1,
				kindDeployment:  deploymentObjs2,
				kindNonExistent: noneExistent1,
			},
			Excepted: &OwnerRef{Kind: kindNonExistent, Name: noneExistentName1},
		},
	}

	for _, c := range cases {
		parent := Lookup(ObjectID{Name: c.PodName, Namespace: c.Namespace}, c.PodObj, c.Refs)
		if c.Excepted == nil {
			assert.Nil(t, parent)
			continue
		}
		assert.Equal(t, *c.Excepted, *parent)
	}
}

func TestLookupOnce(t *testing.T) {
	cases := []struct {
		PodName   string
		PodObj    *PodMap
		Namespace string
		Refs      map[string]*Objects
		Excepted  *OwnerRef
	}{
		{
			PodName:   podName1,
			PodObj:    podObjs1,
			Namespace: namespaceDefault,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs1,
				kindDeployment: deploymentObjs1,
			},
			Excepted: &OwnerRef{Kind: kindReplicaSet, Name: replicaSetName1},
		},
		{
			PodName:   podName1,
			PodObj:    podObjs1,
			Namespace: namespaceOther,
			Refs: map[string]*Objects{
				kindReplicaSet: replicaSetObjs1,
				kindDeployment: deploymentObjs1,
			},
			Excepted: &OwnerRef{Kind: kindPod, Name: podName1},
		},
	}

	for _, c := range cases {
		parent := LookupOnce(ObjectID{Name: c.PodName, Namespace: c.Namespace}, c.PodObj, c.Refs)
		if c.Excepted == nil {
			assert.Nil(t, parent)
			continue
		}
		assert.Equal(t, *c.Excepted, *parent)
	}
}
