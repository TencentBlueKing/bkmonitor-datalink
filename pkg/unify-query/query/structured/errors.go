// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"github.com/pkg/errors"
)

var (
	ErrWrongTableIDFormat = errors.New("wrong table id format > 2")
	ErrEmptyTableID       = errors.New("empty table id")
	ErrNotExistTableID    = errors.New("table id is not exist")

	ErrFieldAndConditionListNotMatch = errors.New("field list and condition list is not match")
	ErrUnknownConditionOperator      = errors.New("unknown condition operator")
	ErrNotAllowedReferenceNameFormat = errors.New("wrong reference name format,which start with _")
	ErrAggMethodNotFound             = errors.New("cannot found aggregate method")
	ErrExprNotAllow                  = errors.New("expr not allow")
	ErrUnknownVargType               = errors.New("unknown varg type")
	ErrMetricMissing                 = errors.New("metric missing")
	ErrMissingValue                  = errors.New("missing value in condition")

	ErrWrongExpressionFormat = errors.New("wrong expression format")
)
