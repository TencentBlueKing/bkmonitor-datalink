// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pproftranslator

import (
	"bytes"

	"github.com/google/pprof/profile"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pproftranslator/jfr"
)

// Translator pprof 数据类型转换器
//
// 将特定格式数据转换为 Profile
type Translator interface {
	Translate(define.ProfilesRawData) (*define.ProfilesData, error)
}

func NewTranslator(c Config) Translator {
	return &spyNameTranslator{} // TODO(mando): 暂时只支持 'spy' 类型
}

type spyNameTranslator struct{}

func (s *spyNameTranslator) Translate(r define.ProfilesRawData) (*define.ProfilesData, error) {
	switch r.Metadata.Format {
	case define.FormatJFR:
		var translator jfr.Translator
		return translator.Translate(r)

	default:
		var translator defaultTranslator
		return translator.Translate(r)
	}
}

type defaultTranslator struct{}

func (d *defaultTranslator) Translate(pd define.ProfilesRawData) (*define.ProfilesData, error) {
	meta := pd.Metadata
	rawData, ok := pd.Data.(define.ProfilePprofFormatOrigin)
	if !ok {
		return nil, errors.Errorf("invalid profile dataType (%T), app: %d-%s", pd.Data, meta.BkBizID, meta.AppName)
	}
	if len(rawData) == 0 {
		return nil, errors.Errorf("empty profile data, app: %d-%s", meta.BkBizID, meta.AppName)
	}

	var buf bytes.Buffer
	buf.Write(rawData)
	pp, err := profile.Parse(&buf)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse profile")
	}

	return &define.ProfilesData{
		Metadata: meta,
		Profiles: []*profile.Profile{pp},
	}, nil
}
