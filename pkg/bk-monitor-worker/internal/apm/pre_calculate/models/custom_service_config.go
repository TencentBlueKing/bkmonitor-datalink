// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	MatchTypeAuto   = "auto"
	MatchTypeManual = "manual"

	PeerServiceDestination = "peerService"
)

// CustomServiceRule rule for custom service, this struct has no difference with bk-collector.
type CustomServiceRule struct {
	Type         string
	Kind         string
	Service      string
	MatchType    string
	MatchKey     core.CommonField
	PredicateKey core.CommonField
	MatchConfig  MatchConfig
	MatchGroups  []MatchGroup

	re       *regexp.Regexp
	mappings map[string]string
}

type MatchConfig struct {
	Regex  string      `json:"regex"`
	Host   RuleHost    `json:"host"`
	Path   RulePath    `json:"path"`
	Params []RuleParam `json:"params"`
}

type MatchGroup struct {
	Source      string
	Destination string
}

type RuleHost struct {
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type RulePath struct {
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type RuleParam struct {
	Name     string `json:"name"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type Op string

// match_op 支持：reg/eq/nq/startswith/nstartswith/endswith/nendswith/contains/ncontains
const (
	OpReg         Op = "reg"
	OpEq          Op = "eq"
	OpNq          Op = "nq"
	OpStartsWith  Op = "startswith"
	OpNStartsWith Op = "nstartswith"
	OpEndsWith    Op = "endswith"
	OpNEndsWith   Op = "nendswith"
	OpContains    Op = "contains"
	OpNContains   Op = "ncontains"
)

func OperatorMatch(input, expected string, op string) bool {
	switch Op(op) {
	case OpReg:
		matched, err := regexp.MatchString(expected, input)
		if err != nil {
			return false
		}
		return matched
	case OpEq:
		return input == expected
	case OpNq:
		return input != expected
	case OpStartsWith:
		return strings.HasPrefix(input, expected)
	case OpNStartsWith:
		return !strings.HasPrefix(input, expected)
	case OpEndsWith:
		return strings.HasSuffix(input, expected)
	case OpNEndsWith:
		return !strings.HasSuffix(input, expected)
	case OpContains:
		return strings.Contains(input, expected)
	case OpNContains:
		return !strings.Contains(input, expected)
	}
	return false
}

func (r *CustomServiceRule) Match(val string) (map[string]string, bool, string) {
	switch r.MatchType {
	case MatchTypeManual:
		mappings, matched := r.ManualMatched(val)
		return mappings, matched, MatchTypeManual
	default:
		mappings, matched := r.AutoMatched(val)
		return mappings, matched, MatchTypeAuto
	}
}

func (r *CustomServiceRule) ManualMatched(val string) (map[string]string, bool) {
	u, err := url.Parse(val)
	if err != nil {
		logger.Warnf("failed to parse url %v, error: %v", val, err)
		return nil, false
	}
	logger.Debugf("parsed url host=%+v, path=%+v, query=%+v", u.Host, u.Path, u.Query())

	if r.MatchConfig.Host.Value != "" {
		if !OperatorMatch(u.Host, r.MatchConfig.Host.Value, r.MatchConfig.Host.Operator) {
			return nil, false
		}
	}

	if r.MatchConfig.Path.Value != "" {
		if !OperatorMatch(u.Path, r.MatchConfig.Path.Value, r.MatchConfig.Path.Operator) {
			return nil, false
		}
	}

	for _, param := range r.MatchConfig.Params {
		val := u.Query().Get(param.Name)
		if val == "" {
			return nil, false
		}
		if !OperatorMatch(val, param.Value, param.Operator) {
			return nil, false
		}
	}

	m := make(map[string]string)
	for _, group := range r.MatchGroups {
		switch group.Source {
		case "peer_service":
			m[group.Destination] = r.Service
		}
	}
	return m, true
}

func (r *CustomServiceRule) AutoMatched(val string) (map[string]string, bool) {
	u, err := url.Parse(val)
	if err != nil {
		return nil, false
	}

	if r.re == nil {
		return nil, false
	}

	match := r.re.FindStringSubmatch(u.String())
	groups := make(map[string]string)
	for i, name := range r.re.SubexpNames() {
		if i != 0 && name != "" && len(match) > i {
			groups[name] = match[i]
		}
	}
	if len(groups) <= 0 {
		return nil, false
	}

	m := make(map[string]string)
	for k, v := range groups {
		if mappingKey, ok := r.mappings[k]; ok {
			m[mappingKey] = v
		}
	}
	return m, true
}

//go:generate goqueryset -in custom_service_config.go -out qs_custom_service_config_gen.go

// CustomServiceConfig This model in BMW only able to query.
// gen:qs
type CustomServiceConfig struct {
	Id          int         `gorm:"primary_key" json:"id"`
	BkBizId     int         `json:"bk_biz_id"`
	AppName     string      `json:"app_name"`
	ConfigLevel string      `json:"config_level"`
	ConfigKey   string      `json:"config_key"`
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	Rule        MatchConfig `json:"rule"`
	MatchType   string      `json:"match_type"`
}

func (r *MatchConfig) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, &r)
}

func (*CustomServiceConfig) TableName() string {
	return "apm_customserviceconfig"
}

func (c *CustomServiceConfig) ToRule() CustomServiceRule {
	instance := CustomServiceRule{
		Type: c.Type,
		// fixed value
		Kind:      "SPAN_KIND_CLIENT",
		Service:   c.Name,
		MatchType: c.MatchType,
		// fixed value
		MatchKey:     core.HttpUrlField,
		PredicateKey: core.HttpMethodField,
		MatchConfig:  c.Rule,
		MatchGroups: []MatchGroup{
			// we just need to care for peerService field
			{
				Source:      "peer_service",
				Destination: PeerServiceDestination,
			},
		},
	}

	if c.Rule.Regex != "" {
		re, err := regexp.Compile(c.Rule.Regex)
		if err != nil {
			logger.Errorf("Failed to compile regex: %s, error: %s", c.Rule.Regex, err)
		} else {
			instance.re = re
		}
	}

	if c.MatchType == MatchTypeAuto {
		mappings := make(map[string]string)
		for _, group := range instance.MatchGroups {
			mappings[group.Source] = group.Destination
		}
		instance.mappings = mappings
	}

	return instance
}
