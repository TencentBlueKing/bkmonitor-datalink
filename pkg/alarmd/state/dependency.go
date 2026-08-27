// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import "fmt"

type DependencyOperation string

const (
	DependencyOperationLoad  DependencyOperation = "LOAD"
	DependencyOperationWrite DependencyOperation = "WRITE"
)

// DependencyError marks only a storage backend call failure. Validation,
// budget and state-shape errors remain ordinary errors and must not be retried
// as infrastructure failures by M7.
type DependencyError struct {
	Operation DependencyOperation
	Target    string
	Err       error
}

func (err *DependencyError) Error() string {
	if err == nil {
		return "state: storage dependency failure"
	}
	return fmt.Sprintf("state: %s target %s: %v", err.Operation, err.Target, err.Err)
}

func (err *DependencyError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}
