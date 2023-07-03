// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"bytes"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
)

// reportV1Config V1 版本的 report 配置
// Note: 本 package 提供了兼容行为 后续版本应该会移除此段逻辑（讲道理的话）
type reportV1Config struct {
	Type          string           `config:"type"`
	DataIDConfigs []reportV1DataID `config:"config_list"`
}

type reportV1DataID struct {
	DataId              int64  `config:"dataid"`
	DataType            string `config:"datatype"`
	Version             string `config:"version"`
	Rate                int64  `config:"rate"`
	AccessToken         string `config:"accesstoken"`
	MaxFutureTimeOffset int64  `config:"max_future_time_offset"`
}

const reportV2Template = `
type: "report_v2"
token: "{{ .AccessToken }}"

default:
  processor:
    - name: "token_checker/proxy"
      config:
        type: "proxy"
        proxy_dataid: {{ .DataId }}
        proxy_token: "{{ .AccessToken }}"

    - name: "rate_limiter/token_bucket"
      config:
        type: token_bucket
        qps: {{ .Rate }}
        burst: {{ .Rate }}

    - name: "proxy_validator/common"
      config:
        type: "{{ .DataType }}"
        version: "{{ .Version }}"
        max_future_time_offset: {{ .MaxFutureTimeOffset }}
`

func convertReportV1ToV2(v1Conf reportV1Config) ([]*confengine.Config, error) {
	tmpl, err := template.New("reportV2").Parse(reportV2Template)
	if err != nil {
		return nil, err
	}

	configs := make([]*confengine.Config, 0)
	for _, conf := range v1Conf.DataIDConfigs {
		buf := &bytes.Buffer{}
		if err := tmpl.Execute(buf, conf); err != nil {
			return nil, err
		}

		loaded, err := confengine.LoadConfigContent(buf.String())
		if err != nil {
			return nil, err
		}
		configs = append(configs, loaded)
	}
	return configs, nil
}

// stealConfigs 偷取 bk-collector 统计目录下的 bkmonitorproxy report 配置文件
// Note: 后续版本中会移除此逻辑
func stealConfigs(patterns []string) []string {
	dst := make([]string, 0)

	const (
		collectorPath = "/bk-collector"
		proxyPath     = "/bkmonitorproxy"
		proxyPattern  = "bkmonitorproxy_report*.conf"
	)

	for _, pattern := range patterns {
		dst = append(dst, pattern)
		p := filepath.Dir(pattern)
		if strings.Contains(p, collectorPath) {
			dst = append(dst, filepath.Join(strings.ReplaceAll(p, collectorPath, proxyPath), proxyPattern))
		}
	}
	return dst
}
