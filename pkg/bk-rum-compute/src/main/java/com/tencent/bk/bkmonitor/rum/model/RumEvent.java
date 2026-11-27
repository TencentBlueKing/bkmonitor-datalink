// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.model;

import com.fasterxml.jackson.annotation.JsonIgnore;
import javax.annotation.Nullable;
import lombok.Getter;
import lombok.Setter;

/**
 * 一条归一化后的 RUM(Real User Monitoring)事件。
 *
 * <p>由 {@code RumEventDeserializationSchema} 从采集网关 envelope(span 形式)或早期 flat event JSON 解析而来。一条
 * Kafka record 中的 {@code items[]} 可能展开成多条 RumEvent。
 *
 * <p>字段映射规则详见 README 中的「事件格式 / 核心映射」章节。
 */
@Setter
@Getter
public class RumEvent {

  @Nullable
  private Span span;

  @Nullable
  private Resource resource;

  /** 事件开始时间（微秒 epoch，全链路统一单位）。 */
  private long eventTime;

  /** 事件结束时间（微秒 epoch）；上游缺失或非法时回退为 {@link #eventTime}。 */
  private long endTime;

  @Nullable
  private SpanStatus status;

  @Nullable
  private String sessionId;

  @Nullable
  private String spanType;

  private long hasSessionAndTime;

  /** 是否具备进入会话聚合的最小条件。 */
  @JsonIgnore
  public boolean hasSessionAndTime() {
    return sessionId != null && !sessionId.trim().isEmpty() && eventTime > 0L;
  }
}
