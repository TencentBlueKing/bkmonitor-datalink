// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package transport

import (
	"errors"
)

var (
	ErrQueryFailed            = errors.New("query failed")
	ErrGetTimestampFailed     = errors.New("get timestamp failed")
	ErrConvertTimestampFailed = errors.New("convert timestamp failed")
	ErrWriteFailed            = errors.New("write failed")
	ErrCtxInterrupted         = errors.New("context done")
	ErrConvertTypeFailed      = errors.New("convert to number failed")
	ErrQueryFieldFailed       = errors.New("query field failed")
	ErrQueryTagFailed         = errors.New("query tag failed")
	ErrTypeIsNil              = errors.New("type is nil")
	ErrQueryOverflow          = errors.New("query got too much data")
	ErrTooSmallDuration       = errors.New("too small duration")
)
