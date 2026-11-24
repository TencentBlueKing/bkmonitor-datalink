// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sourceliveness

// WithMaxInFlight is the Checker running at most n probes at once, for a
// test that reaches the cap without sixteen.
func (checker *Checker) WithMaxInFlight(n int) *Checker {
	checker.maxInFlight = n
	return checker
}

// WithMaxHeld is the Checker remembering at most n held Levels.
func (checker *Checker) WithMaxHeld(n int) *Checker {
	checker.held.max = n
	return checker
}

// WithMaxAnswers is the Checker keeping at most n answers, for a test that
// reaches the bound without 4096 sources.
func (checker *Checker) WithMaxAnswers(n int) *Checker {
	checker.maxAnswers = n
	return checker
}
