// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package errno

import (
	"fmt"

	"github.com/samber/lo"
)

const (
	CodeField      = "错误码"
	MessageField   = "消息"
	CategoryField  = "分类"
	ComponentField = "组件"
	OperationField = "操作"
	SolutionField  = "解决方案"
	SeverityField  = "严重程度"
)

type ErrCode struct {
	context map[string]any // 统一的上下文信息，包含所有字段
	err     error          // 包装的原始错误
}

func NewErrCode(code, message, category string) *ErrCode {
	return &ErrCode{
		context: map[string]any{
			CodeField:     code,
			MessageField:  message,
			CategoryField: category,
		},
	}
}

func (e *ErrCode) String() string {
	parts := []string{fmt.Sprintf("%s [%s]", e.Message(), e.Code())}
	fieldMap := map[string]string{
		CategoryField:  "分类",
		ComponentField: "组件",
		OperationField: "操作",
		SolutionField:  "解决方案",
		SeverityField:  "严重程度",
	}

	for field, label := range fieldMap {
		if value := e.getString(field); value != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", label, value))
		}
	}

	excludeFields := []string{CategoryField, ComponentField, OperationField, SolutionField, SeverityField}

	customFields := lo.MapToSlice(e.context, func(key string, value any) string {
		if lo.Contains(excludeFields, key) {
			return ""
		}
		return fmt.Sprintf("%s: %v", key, value)
	})

	customFields = lo.Filter(customFields, func(field string, _ int) bool {
		return field != ""
	})
	parts = append(parts, customFields...)

	if e.err != nil {
		if childErr, ok := e.err.(*ErrCode); ok {
			parts = append(parts, fmt.Sprintf("[%s]", childErr.String()))
		} else {
			parts = append(parts, fmt.Sprintf("错误: %v", e.err))
		}
	}

	return lo.Reduce(parts, func(acc string, part string, index int) string {
		if index == 0 {
			return part
		}
		return acc + " | " + part
	}, "")
}

func (e *ErrCode) Error() string {
	return e.String()
}

func (e *ErrCode) Code() string { return e.getString(CodeField) }

func (e *ErrCode) Message() string { return e.getString(MessageField) }

func (e *ErrCode) Category() string { return e.getString(CategoryField) }

func (e *ErrCode) Component() string { return e.getString(ComponentField) }

func (e *ErrCode) Operation() string { return e.getString(OperationField) }

func (e *ErrCode) Solution() string { return e.getString(SolutionField) }

func (e *ErrCode) Severity() string { return e.getString(SeverityField) }

func (e *ErrCode) getString(key string) string {
	if v, ok := e.context[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (e *ErrCode) WithComponent(component string) *ErrCode {
	e.context[ComponentField] = component
	return e
}

func (e *ErrCode) WithOperation(operation string) *ErrCode {
	e.context[OperationField] = operation
	return e
}

func (e *ErrCode) WithSolution(solution string) *ErrCode {
	e.context[SolutionField] = solution
	return e
}

func (e *ErrCode) WithSeverity(severity string) *ErrCode {
	e.context[SeverityField] = severity
	return e
}

func (e *ErrCode) WithContext(key string, value any) *ErrCode {
	e.context[key] = value
	return e
}

func (e *ErrCode) WithContexts(contexts map[string]any) *ErrCode {
	for k, v := range contexts {
		e.context[k] = v
	}
	return e
}

func (e *ErrCode) SetField(key, value string) *ErrCode {
	e.context[key] = value
	return e
}

func (e *ErrCode) GetField(key string) any {
	return e.context[key]
}

func (e *ErrCode) WithError(err error) *ErrCode {
	e.err = err
	return e
}

func (e *ErrCode) WithErrorf(format string, args ...any) *ErrCode {
	e.err = fmt.Errorf(format, args...)
	return e
}

func (e *ErrCode) WithDetail(key string, value any) *ErrCode {
	return e.WithContext(key, value)
}

func (e *ErrCode) WithParam(paramType string) *ErrCode {
	return e.WithContext("参数类型", paramType)
}

func (e *ErrCode) Unwrap() error {
	return e.err
}

func (e *ErrCode) FormatLogMessage(details map[string]any) string {
	msg := fmt.Sprintf("%s [%s]", e.Message(), e.Code())

	if e.Component() != "" {
		msg += fmt.Sprintf(" | 存储: %s", e.Component())
	}

	if e.Operation() != "" {
		msg += fmt.Sprintf(" | 操作: %s", e.Operation())
	}

	for key, value := range details {
		msg += fmt.Sprintf(" | %s: %v", key, value)
	}

	if e.Solution() != "" {
		msg += fmt.Sprintf(" | 解决: %s", e.Solution())
	}

	return msg
}
