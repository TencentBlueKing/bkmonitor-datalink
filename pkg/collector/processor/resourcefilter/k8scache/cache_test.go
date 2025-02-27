// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package k8scache

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
)

func TestCache(t *testing.T) {
	rsp := response{
		ResourceVersion: 4,
		Pods: []podObject{
			{
				Action:    "CreateOrUpdate",
				ClusterID: "BCS-K8S-00000",
				Name:      "bkm-statefulset-worker-0",
				Namespace: "bkmonitor-operator",
				IP:        "127.0.0.1",
			},
			{
				Action:    "CreateOrUpdate",
				ClusterID: "BCS-K8S-00000",
				Name:      "bkm-statefulset-worker-1",
				Namespace: "bkmonitor-operator",
				IP:        "127.0.0.2",
			},
		},
	}

	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(rsp)
		w.Write(b)
	}))
	defer svr.Close()

	c := New(&Config{
		URL: svr.URL,
	})
	c.Sync()
	defer c.Clean()

	// wait
	time.Sleep(time.Second)

	var v map[string]string
	var ok bool

	v, ok = c.Get("127.0.0.1")
	assert.True(t, ok)
	assert.Equal(t, "bkm-statefulset-worker-0", v["k8s.pod.name"])

	v, ok = c.Get("127.0.0.2")
	assert.True(t, ok)
	assert.Equal(t, "bkm-statefulset-worker-1", v["k8s.pod.name"])

	v, ok = c.Get("127.0.0.3")
	assert.False(t, ok)
}
