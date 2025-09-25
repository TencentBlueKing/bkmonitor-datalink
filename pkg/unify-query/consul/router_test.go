// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"testing"

	"github.com/hashicorp/consul/api"
	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestBCS_Info
func TestBCS_Info(t *testing.T) {
	log.InitTestLogger()
	_ = SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", nil,
	)
	BCSInfoPath = "test/metadata/v1/default/project_id"
	kv := api.KVPairs{
		{
			Key:   BCSInfoPath + "/aaaaa/cluster_id/bcs-k8s",
			Value: []byte(`[1500009, 1500011]`),
		},
		{
			Key:   BCSInfoPath + "/aaaaa/cluster_id/bcs-k9s",
			Value: []byte(`[1500013]`),
		},
		{
			Key:   BCSInfoPath + "/bbbbb/cluster_id/bcs-k10s",
			Value: []byte(`[1500033]`),
		},
	}

	expects := BCSInfo{
		info: map[string]map[string][]DataID{
			"aaaaa": {
				"bcs-k8s": {1500009, 1500011},
				"bcs-k9s": {1500013},
			},
			"bbbbb": {
				"bcs-k10s": {1500033},
			},
		},
	}

	stubs := gostub.StubFunc(&GetDataWithPrefix, kv, nil)
	defer stubs.Reset()

	err := ReloadBCSInfo()
	assert.Nil(t, err)
	assert.Equal(t, bcsInfo.info, expects.info)
}
