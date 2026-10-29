// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"

// queryOutcome is an attempt's span ending: the completeness it reached and
// the attempt's reason code, both closed vocabularies, never an error's text,
// which can carry addresses.
func queryOutcome(completion execution.ProviderCompletion, err error) (string, string, bool) {
	if err != nil {
		return "ERROR", "", true
	}
	reason := ""
	if attempts := completion.RouteFacts.Attempts; len(attempts) > 0 {
		reason = string(attempts[len(attempts)-1].ReasonCode)
	}
	return string(completion.Completeness), reason, completion.Completeness == execution.CompletenessUnavailable
}
