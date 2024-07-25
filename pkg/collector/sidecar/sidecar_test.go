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

func TestGetPrivilegedConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{
		ctx:     ctx,
		cancel:  cancel,
		dataids: make(map[string]IDSpec),
	}

	s := &Sidecar{
		ctx:     ctx,
		cancel:  cancel,
		watcher: w,
	}

	var (
		expectedConf privilegedConfig
		expectedIDs  []IDSpec
	)

	// insert test1
	w.HandleDataIDAdd(&bkv1beta1.DataID{
		ObjectMeta: metav1.ObjectMeta{
			Name: "id-1001",
			Labels: map[string]string{
				keyUsage:    "collector.metrics",
				keyScope:    "privileged",
				keyTokenRef: "test-token",
			},
		},
		Spec: bkv1beta1.DataIDSpec{
			DataID: 1001,
		},
	})

	expectedConf = s.getPrivilegedConfig()
	expectedIDs = w.DataIDs()
	assert.Equal(t, len(expectedIDs), 1)
	assert.Equal(t, expectedConf.MetricsDataID, 1001)
	assert.Equal(t, expectedConf.BizID, int32(0))
	assert.Equal(t, expectedConf.AppName, "")

	// insert test2
	w.HandleDataIDAdd(&bkv1beta1.DataID{
		ObjectMeta: metav1.ObjectMeta{
			Name: "id-1002",
			Labels: map[string]string{
				keyUsage:    "collector.traces",
				keyScope:    "privileged",
				keyTokenRef: "test-token",
				keyBizID:    "2",
				keyAppName:  "bk_app_name",
			},
		},
		Spec: bkv1beta1.DataIDSpec{
			DataID: 1002,
		},
	})

	expectedConf = s.getPrivilegedConfig()
	expectedIDs = w.DataIDs()
	assert.Equal(t, len(expectedIDs), 2)
	assert.Equal(t, expectedConf.MetricsDataID, 1001)
	assert.Equal(t, expectedConf.TracesDataID, 1002)
	assert.Equal(t, expectedConf.BizID, int32(2))
	assert.Equal(t, expectedConf.AppName, "bk_app_name")
}
