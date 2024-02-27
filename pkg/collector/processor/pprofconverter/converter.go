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

type Pprofable interface{}

type DefaultPprofable struct{}

// Parse 默认 pprof 数据格式
func (d *DefaultPprofable) Parse(pd define.ProfilesRawData) (*define.ProfilesData, error) {
	rawData, ok := pd.Data.(define.ProfilePprofFormatOrigin)
	if !ok {
		return nil, errors.Errorf(
			"invalid profile data, skip. rawDataType: %T app: %d-%s",
			pd.Data, pd.Metadata.BkBizID, pd.Metadata.AppName,
		)
	}
	if len(rawData) == 0 {
		return nil, errors.Errorf(
			"empty profile data, skip. app: %d-%s", pd.Metadata.BkBizID, pd.Metadata.AppName,
		)
	}

	var buf bytes.Buffer
	buf.Write(rawData)
	pp, err := profile.Parse(&buf)
	if err != nil {
		return nil, errors.Errorf("failed to parse profile, error: %s", err)
	}

	return &define.ProfilesData{Metadata: pd.Metadata, Profiles: []*profile.Profile{pp}}, nil
}

func NewPprofConverter(c Config) PprofConverter {
	switch c.Type {
	case "spy_converter":
		return &spyNameJudgeConverter{}
	default:
		return &spyNameJudgeConverter{}
	}
}

type PprofConverter interface {
	// Parse 将特定格式数据转换为 Profile
	Parse(define.ProfilesRawData) (*define.ProfilesData, error)
}

type spyNameJudgeConverter struct{}

func (s *spyNameJudgeConverter) Parse(r define.ProfilesRawData) (*define.ProfilesData, error) {
	switch r.Metadata.Format {
	case define.FormatJFR:
		converter := jfr.Converter{}
		return converter.Parse(r)
	default:
		converter := DefaultPprofable{}
		return converter.Parse(r)
	}
}
