// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import "fmt"

// InternalError reports a broken module invariant. It is not a business
// terminal and therefore must leave the current message unfinished.
type InternalError struct {
	Operation string
	PlanID    string
	Err       error
}

func (e *InternalError) Error() string {
	if e == nil {
		return "alarmd detect: internal error"
	}
	if e.PlanID == "" {
		return fmt.Sprintf("alarmd detect: %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("alarmd detect: %s plan %s: %v", e.Operation, e.PlanID, e.Err)
}

func (e *InternalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// BudgetError reports a deterministic execution budget rejection. The caller
// must not commit a partial DetectionBatch.
type BudgetError struct {
	Budget string
	Limit  uint64
	Actual uint64
}

func (e *BudgetError) Error() string {
	if e == nil {
		return "alarmd detect: execution budget exceeded"
	}
	return fmt.Sprintf("alarmd detect: %s budget exceeded: actual=%d limit=%d", e.Budget, e.Actual, e.Limit)
}

// ControlledError is an executor-declared, record-local failure. Only reason
// codes frozen in DetectorSpec may be converted into an ERROR LevelFact.
type ControlledError struct {
	ReasonCode string
}

func (e *ControlledError) Error() string {
	if e == nil || e.ReasonCode == "" {
		return "alarmd detect: controlled detector error"
	}
	return "alarmd detect: controlled detector error: " + e.ReasonCode
}
