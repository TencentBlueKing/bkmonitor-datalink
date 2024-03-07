// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"regexp"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/module"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type EventProcessor struct {
	cfg     keyword.ProcessConfig
	decoder *encoding.Decoder // lower string

	filterRegs []*regexp.Regexp
	rules      map[string]*regexp.Regexp
}

func NewEventProcessor(cfg keyword.ProcessConfig) (*EventProcessor, error) {
	p := &EventProcessor{
		cfg: cfg,
	}

	// default encoding utf-8, decoder will be nil
	if cfg.Encoding == configs.EncodingGBK {
		p.decoder = simplifiedchinese.GB18030.NewDecoder()
	}

	p.cfg.HasFilter = len(cfg.FilterPatterns) > 0

	// Precompiled  filterConfig
	p.filterRegs = make([]*regexp.Regexp, 0, len(cfg.FilterPatterns))
	for _, fp := range cfg.FilterPatterns {
		regex, err := regexp.Compile(fp)
		if err != nil {
			logger.Errorf("keyword filter config error. "+
				"DataID:(%d), Pattern:(%s), Error:%v", cfg.DataID, fp, err)
			continue
		}

		p.filterRegs = append(p.filterRegs, regex)
	}

	// Precompiled
	p.rules = make(map[string]*regexp.Regexp)
	for _, kfc := range cfg.KeywordConfigs {
		regex, err := regexp.Compile(kfc.Pattern)
		if err != nil {
			logger.Errorf("keywords config error. "+
				"DataID:%d, Name:%s, Pattern:%s, Error:%v",
				cfg.DataID, kfc.Name, kfc.Pattern, err)
			continue
		}

		p.rules[kfc.Name] = regex
	}

	return p, nil
}

func (client *EventProcessor) Filter(event *module.LogEvent) bool {
	if !client.cfg.HasFilter {
		return false
	}

	for _, filterRegex := range client.filterRegs {
		if filterRegex.MatchString(event.Text) {
			logger.Debugf("event(%s) are filtered by reg:(%s)",
				event.Text, filterRegex.String())
			return true
		}
	}
	return false
}

func (client *EventProcessor) Handle(event *module.LogEvent) (interface{}, error) {
	results := make([]keyword.KeywordTaskResult, 0, len(client.rules))

	logger.Debugf("get event, %v", event)
	if client.decoder != nil {
		event.Text, _ = client.decoder.String(event.Text)
	}

	if client.Filter(event) {
		return results, nil
	}

	for ruleName, ruleRegex := range client.rules {
		fields := ruleRegex.SubexpNames()
		count := len(fields)
		matched := ruleRegex.FindStringSubmatch(event.Text)
		if matched != nil {
			dimensions := make(map[string]string, count)
			dimensionFields := make([]string, 0)
			for i := 1; i < count; i++ {
				fieldName := fields[i]
				if len(fieldName) == 0 {
					// 去掉未命名的字段
					continue
				}
				dimensions[fields[i]] = matched[i]
				dimensionFields = append(dimensionFields, fields[i])
			}
			res := keyword.KeywordTaskResult{
				FilePath:     event.File.State.Source,
				RuleName:     ruleName,
				SortedFields: dimensionFields,
				Dimensions:   dimensions,
				Log:          event.Text,
			}
			results = append(results, res)
		}
	}

	logger.Debugf("return event, count(%d), %v", len(results), results)
	return results, nil
}

func (client *EventProcessor) Send(event interface{}, outputs []chan<- interface{}) {
	results, ok := event.([]keyword.KeywordTaskResult)
	if !ok {
		logger.Errorf("Keyword Processor output result format not correct")
		return
	}

	for _, result := range results {
		for _, output := range outputs {
			output <- result
		}
	}
}
