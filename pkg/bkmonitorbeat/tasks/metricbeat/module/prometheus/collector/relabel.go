// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"gopkg.in/yaml.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RemoteRelabelConfig 远程获取 relabel 配置
type RemoteRelabelConfig struct {
	SourceLabels []string `json:"sourceLabels"`
	Separator    string   `json:"separator"`
	Regex        string   `json:"regex"`
	Modulus      uint64   `json:"modulus"`
	TargetLabel  string   `json:"targetLabel"`
	Replacement  string   `json:"replacement"`
	Action       string   `json:"action"`
}

func (m *MetricSet) getRemoteRelabelConfigs() ([]*relabel.Config, error) {
	resp, err := m.remoteClient.Get(m.MetricRelabelRemote)
	if err != nil {
		return nil, err
	}

	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, resp.Body); err != nil {
		return nil, err
	}

	configs := make([]RemoteRelabelConfig, 0)
	if err := json.Unmarshal(buf.Bytes(), &configs); err != nil {
		return nil, err
	}

	ret := make([]*relabel.Config, 0)
	for _, conf := range configs {
		re, err := relabel.NewRegexp(conf.Regex)
		if err != nil {
			logger.Errorf("failed to compile relabel config regex %s, error: %v", conf.Regex, err)
			continue
		}

		lbs := make([]model.LabelName, 0)
		for _, lb := range conf.SourceLabels {
			lbs = append(lbs, model.LabelName(lb))
		}
		ret = append(ret, &relabel.Config{
			SourceLabels: lbs,
			Separator:    conf.Separator,
			Regex:        re,
			Modulus:      conf.Modulus,
			TargetLabel:  conf.TargetLabel,
			Replacement:  conf.Replacement,
			Action:       relabel.Action(conf.Action),
		})
	}

	return ret, nil
}

func (m *MetricSet) metricRelabel(promEvent *tasks.PromEvent) bool {
	promLabels := make(labels.Labels, 0)
	for sourceKey, sourceValue := range promEvent.Labels {
		promLabels = append(promLabels, labels.Label{
			Name:  sourceKey,
			Value: sourceValue.(string),
		})
	}

	promLabels = append(promLabels, labels.Label{
		Name:  metricName,
		Value: promEvent.Key,
	})

	// up metric 不做 relabels 调整
	if IsInnerMetric(promEvent.Key) {
		return true
	}

	// 判断指标是否被过滤
	lset, _ := relabel.Process(promLabels, m.MetricRelabelConfigs...)
	if len(lset) == 0 {
		logger.Debugf("data: %v skipped by metric relabel config", promLabels)
		return false
	}

	if len(m.remoteRelabelCache) > 0 {
		lset, _ = relabel.Process(lset, m.remoteRelabelCache...)
	}

	// 基于过滤结果，将数据重新收集
	promEvent.Key = ""
	promEvent.Labels = make(common.MapStr)
	for _, label := range lset {
		if label.Name == metricName {
			promEvent.Key = label.Value
			continue
		}
		promEvent.Labels[label.Name] = label.Value
	}

	if promEvent.Key == "" {
		return false
	}
	return true
}

// promRelabel prometheus 提供的内置 relabels 配置
type promRelabel struct {
	// A list of labels from which values are taken and concatenated
	// with the configured separator in order.
	SourceLabels []string `yaml:"source_labels,flow,omitempty"`
	// Separator is the string between concatenated values from the source labels.
	Separator string `yaml:"separator,omitempty"`
	// Regex against which the concatenation is matched.
	Regex string `yaml:"regex,omitempty"`
	// Modulus to take of the hash of concatenated values from the source labels.
	Modulus uint64 `yaml:"modulus,omitempty"`
	// TargetLabel is the label to which the resulting string is written in a replacement.
	// Regexp interpolation is allowed for the replace action.
	TargetLabel string `yaml:"target_label,omitempty"`
	// Replacement is the regex replacement pattern to be used.
	Replacement string `yaml:"replacement,omitempty"`
	// Action is the action to be performed for the relabeling.
	Action string `yaml:"action,omitempty"`
}

func handleRelabels(configs interface{}) ([]promRelabel, *ActionConfigs, error) {
	var relabels []promRelabel
	data, err := yaml.Marshal(configs)
	if err != nil {
		return nil, nil, err
	}
	if err = yaml.Unmarshal(data, &relabels); err != nil {
		return nil, nil, err
	}

	dst := make([]promRelabel, 0)
	ac := &ActionConfigs{}

	for i := 0; i < len(relabels); i++ {
		rl := relabels[i]
		switch rl.Action {
		case ActionTypeRate:
			if len(rl.SourceLabels) == 1 && rl.SourceLabels[0] == model.MetricNameLabel {
				ac.Rate = append(ac.Rate, ActionRate{
					Source:      rl.Regex, // 借用 Regex 字段，实际并不支持正则
					Destination: rl.Replacement,
				})
			}

		case ActionTypeDelta:
			ac.Delta = append(ac.Delta, rl.SourceLabels...)

		default: // prometheus 标准 actions
			dst = append(dst, rl)
		}
	}
	return dst, ac, nil
}
