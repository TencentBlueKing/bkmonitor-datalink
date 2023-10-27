// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apdexcalculator

const (
	apdexSatisfied  = "satisfied"
	apdexTolerating = "tolerating"
	apdexFrustrated = "frustrated"
)

type Calculator interface {
	Calc(val, threshold float64) string
}

func NewCalculator(c Config) Calculator {
	switch c.Calculator.Type {
	case "fixed":
		return fixedCalculator{c.Calculator.ApdexStatus}
	default:
		return standardCalculator{}
	}
}

type standardCalculator struct{}

func (c standardCalculator) Calc(val, threshold float64) string {
	threshold = threshold * 1e6 // ms -> ns
	switch {
	case val <= threshold:
		return apdexSatisfied
	case val <= 4*threshold:
		return apdexTolerating
	default:
		return apdexFrustrated
	}
}

type fixedCalculator struct {
	s string
}

func (c fixedCalculator) Calc(_, _ float64) string {
	return c.s
}
