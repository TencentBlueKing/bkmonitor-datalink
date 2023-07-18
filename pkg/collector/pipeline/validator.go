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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Validator struct {
	Func define.PreCheckValidateFunc
}

func (v Validator) Validate(r *define.Record) (define.StatusCode, string, error) {
	if v.Func != nil {
		return v.Func(r)
	}
	return validatePreCheckProcessors(r, GetDefaultGetter())
}

func validatePreCheckProcessors(r *define.Record, getter Getter) (define.StatusCode, string, error) {
	if getter == nil {
		logger.Debug("no pipeline getter found")
		return define.StatusCodeOK, "", nil
	}

	pl := getter.GetPipeline(r.RecordType)
	if pl == nil {
		return define.StatusBadRequest, "", errors.Errorf("unknown pipeline type %v", r.RecordType)
	}

	for _, name := range pl.PreCheckProcessors() {
		inst := getter.GetProcessor(name)
		switch inst.Name() {
		case define.ProcessorTokenChecker:
			if _, err := inst.Process(r); err != nil {
				return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, err
			}

		case define.ProcessorRateLimiter:
			if _, err := inst.Process(r); err != nil {
				return define.StatusCodeTooManyRequests, define.ProcessorRateLimiter, err
			}

		case define.ProcessorProxyValidator:
			if _, err := inst.Process(r); err != nil {
				return define.StatusBadRequest, define.ProcessorProxyValidator, err
			}

		case define.ProcessorLicenseChecker:
			if _, err := inst.Process(r); err != nil {
				return define.StatusBadRequest, define.ProcessorLicenseChecker, err
			}
		}
	}

	return define.StatusCodeOK, "", nil
}
