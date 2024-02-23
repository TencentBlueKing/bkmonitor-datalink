// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pprofconverter

import (
	"bytes"

	"github.com/google/pprof/profile"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pprofconverter/jfr"
)

type Pprofable interface {
	// Type Pprof转换器的类型
	Type() string
	// ParseToPprof 将特定格式数据转换为Profile
	ParseToPprof(define.ProfilesRawData) (*define.ProfilesData, error)
}

type DefaultPprofable struct {
}

func (d *DefaultPprofable) Type() string {
	return define.FormatPprof
}

func (d *DefaultPprofable) ParseToPprof(pd define.ProfilesRawData) (*define.ProfilesData, error) {
	rawData, success := pd.Data.(define.ProfilePprofFormatOrigin)
	if !success {
		return nil, errors.Errorf("invalid profile data, skip")
	}
	if len(rawData) == 0 {
		return nil, errors.Errorf("empty profile data, skip")
	}

	var buf bytes.Buffer
	buf.Write(rawData)
	pp, err := profile.Parse(&buf)
	if err != nil {
		return nil, errors.Errorf("failed to parse profile, error: %s", err)
	}

	return &define.ProfilesData{Metadata: pd.Metadata, Profiles: []*profile.Profile{pp}}, nil
}

func NewPprofConverterEntry(c Config) ConverterEntry {
	switch c.Type {
	case "none":
		return &noneConverterEntry{}
	case "spy_converter":
		return &spyNameJudgeConverterEntry{}
	default:
		return &spyNameJudgeConverterEntry{}
	}
}

type ConverterEntry interface {
	GetConverter(define.ProfilesRawData) Pprofable
}

type spyNameJudgeConverterEntry struct{}

func (s *spyNameJudgeConverterEntry) GetConverter(r define.ProfilesRawData) Pprofable {
	switch r.Metadata.Format {
	case define.FormatJFR:
		return &jfr.Converter{}
	default:
		return &DefaultPprofable{}
	}
}

type noneConverterEntry struct{}

func (n *noneConverterEntry) GetConverter(_ define.ProfilesRawData) Pprofable {
	return &DefaultPprofable{}
}
