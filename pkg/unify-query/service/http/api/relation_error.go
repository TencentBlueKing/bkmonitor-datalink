// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import "errors"

// truncationError 由可向调用方暴露稳定截断原因的错误实现。
type truncationError interface {
	TruncationReason() string
}

// relationTruncationMetadata 从包装后的错误链中提取截断标记及机器可读原因。
func relationTruncationMetadata(err error) (bool, string) {
	var limitErr truncationError
	if !errors.As(err, &limitErr) {
		return false, ""
	}
	return true, limitErr.TruncationReason()
}
