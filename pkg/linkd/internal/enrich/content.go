// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import "errors"

// SourceContentFallbackReason 为描述失败返回诊断码；无法生成描述时保留来源内容，
// 包括空串。创建与只读预览共用此策略，调用方仍须响应 Context 取消。
func SourceContentFallbackReason(err error) string {
	if err == nil {
		return ""
	}
	var failure interface{ PermanentContentFailure() string }
	if errors.As(err, &failure) {
		return failure.PermanentContentFailure()
	}
	return "content_unavailable"
}
