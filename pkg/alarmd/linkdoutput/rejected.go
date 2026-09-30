// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package linkdoutput

import "errors"

// The rules a decision can break on its way to a message, closed. Each is
// one reason Convert refuses one decision; the refusal is about that decision
// alone, so the caller writes the others of its batch and counts this one by
// the rule it broke.
const (
	RuleIdentityMissing  = "standard_identity_missing"
	RuleActionUnknown    = "standard_action_unknown"
	RuleLevelsInvalid    = "standard_levels_invalid"
	RuleTooManyLevels    = "standard_too_many_levels"
	RuleBusinessIdentity = "standard_business_identity"
	RuleEncode           = "standard_encode"
)

// RejectedError is Convert refusing one decision, by the rule it broke.
type RejectedError struct {
	Rule string
	Err  error
}

func (err *RejectedError) Error() string {
	if err == nil || err.Err == nil {
		return "alarmd linkdoutput: decision rejected"
	}
	return err.Err.Error()
}

func (err *RejectedError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func reject(rule string, err error) *RejectedError {
	return &RejectedError{Rule: rule, Err: err}
}

// RuleOf is the rule a Convert error names, and false for an error that is
// not a refusal of one decision.
func RuleOf(err error) (string, bool) {
	var rejected *RejectedError
	if errors.As(err, &rejected) && rejected != nil && rejected.Rule != "" {
		return rejected.Rule, true
	}
	return "", false
}
