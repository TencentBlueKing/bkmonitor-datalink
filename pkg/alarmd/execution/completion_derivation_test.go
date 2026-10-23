// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"

// The completion the production derivation reports, read the ways these
// tests ask about it: the kind alone, the kind with its cause, and the cause
// with its reason. All three are DeriveCompletionAttribution's answer.

func deriveCompletionKind(input execution.InternalExecution, result execution.EvaluationResult) (execution.CompletionKind, error) {
	kind, _, err := execution.DeriveCompletionAttribution(input, result)
	return kind, err
}

func deriveCompletion(input execution.InternalExecution, result execution.EvaluationResult) (execution.CompletionKind, execution.CompletionCause, error) {
	kind, attribution, err := execution.DeriveCompletionAttribution(input, result)
	return kind, attribution.Cause, err
}

func deriveCompletionDetail(input execution.InternalExecution, result execution.EvaluationResult) (
	execution.CompletionKind, execution.CompletionCause, execution.ReasonCode, error) {
	kind, attribution, err := execution.DeriveCompletionAttribution(input, result)
	return kind, attribution.Cause, attribution.Reason, err
}
