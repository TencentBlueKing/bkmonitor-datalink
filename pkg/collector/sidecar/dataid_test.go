// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sidecar

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/crd/v1beta1"
)

var (
	accepted = []*bkv1beta1.DataID{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "id-1001",
				Labels: map[string]string{
					keyUsage:    "collector.traces",
					keyScope:    "privileged",
					keyTokenRef: "test-token",
				},
			},
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1001,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "id-1002",
				Labels: map[string]string{
					keyUsage:    "collector.metrics",
					keyScope:    "privileged",
					keyTokenRef: "test-token",
				},
			},
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1002,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "id-1003",
				Labels: map[string]string{
					keyUsage:    "collector.logs",
					keyScope:    "privileged",
					keyTokenRef: "test-token",
				},
			},
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1003,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "id-1004",
				Labels: map[string]string{
					keyUsage: "collector.traces",
					keyScope: "privileged",
				},
			},
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1004,
			},
		},
	}

	rejected = []*bkv1beta1.DataID{
		{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "unsupported-usage",
				Labels: map[string]string{
					keyUsage: "metrics",
					keyScope: "privileged",
				},
			},
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1005,
			},
		},
		{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "unsupported-usage-dot",
				Labels: map[string]string{
					keyUsage: "collector.foo",
					keyScope: "privileged",
				},
			},
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1005,
			},
		},
		{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "unsupported-scope",
				Labels: map[string]string{
					keyUsage: "collector.metrics",
					keyScope: "platform",
				},
			},
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1006,
			},
		},
	}
)

func TestWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := dataIDWatcher{
		ctx:     ctx,
		cancel:  cancel,
		dataids: make(map[string]IDSpec),
	}

	for _, id := range accepted {
		w.HandleDataIDAdd(id)
	}
	for _, id := range rejected {
		w.HandleDataIDDelete(id)
	}

	t.Run("upsert", func(t *testing.T) {
		ids := w.DataIDs()
		assert.Len(t, ids, 4)
		for idx, id := range w.DataIDs() {
			assert.Equal(t, idx+1001, id.DataID)
		}
	})

	t.Run("delete", func(t *testing.T) {
		w.deleteDataID(accepted[0])
		ids := w.DataIDs()
		assert.Len(t, ids, 3)
		for idx, id := range w.DataIDs() {
			assert.Equal(t, idx+1002, id.DataID)
		}
	})
}
