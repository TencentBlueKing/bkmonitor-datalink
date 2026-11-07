// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import javax.annotation.Nullable;
import lombok.Data;

/**
 * Span 的执行状态,对应 OpenTelemetry Span Status 语义。
 *
 * <p>嵌入在 {@link Span#getStatus()} 中,用于在追踪数据中标记 span 的执行结果。
 */
@Data
public class SpanStatus {
  /**
   * 状态码,取值遵循 OTel 约定:
   *
   * <ul>
   *   <li>{@code 0} UNSET — 未显式设置,等价于 OK
   *   <li>{@code 1} OK — 执行成功
   *   <li>{@code 2} ERROR — 执行出错
   * </ul>
   */
  @Nullable
  @JsonProperty("code")
  private Integer code;

  /** 状态描述,通常仅在 {@link #code} = 2(ERROR)时填写,承载错误信息。 */
  @Nullable
  @JsonProperty("message")
  private String message;
}
