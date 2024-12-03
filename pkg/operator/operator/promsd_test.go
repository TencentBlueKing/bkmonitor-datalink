// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const content = `
- job_name: 'apiserver'
  tls_config:
    insecure_skip_verify: true
  proxy_url: "http://metircs-proxy.kube-system:80"
  scheme: https
  bearer_token: foobar_string
  http_sd_configs:
  - follow_redirects: false
    refresh_interval: 1m
    url: http://metircs-proxy.kube-system:80/sd?com=apiserver
  relabel_configs:
  - source_labels: [__address__]
    separator: ;
    regex: (.*)
    target_label: __address__
    replacement: $1:60002
    action: replace
- job_name: 'scheduler'
  tls_config:
    insecure_skip_verify: true
  proxy_url: "http://metircs-proxy.kube-system:80"
  scheme: https
  bearer_token: foobar_string
  http_sd_configs:
  - follow_redirects: false
    refresh_interval: 1m
    url: http://metircs-proxy.kube-system:80/sd?com=scheduler
  relabel_configs:
  - source_labels: [__address__]
    separator: ;
    regex: (.*)
    target_label: __address__
    replacement: $1:10259
    action: replace
- job_name: 'controller-manager'
  tls_config:
    insecure_skip_verify: true
  proxy_url: "http://metircs-proxy.kube-system:80"
  scheme: https
  bearer_token: foobar_string
  http_sd_configs:
  - follow_redirects: false
    refresh_interval: 1m
    url: http://metircs-proxy.kube-system:80/sd?com=controller-manager
  relabel_configs:
  - source_labels: [__address__]
    separator: ;
    regex: (.*)
    target_label: __address__
    replacement: $1:10257
    action: replace
`

func TestUnmarshalPromSdConfigs(t *testing.T) {
	configs, err := unmarshalPromSdConfigs([]byte(content))
	assert.NoError(t, err)

	assert.Len(t, configs, 3)
	for _, config := range configs {
		t.Logf("config.httpconfig: %+v", config.HTTPClientConfig)
	}
}
