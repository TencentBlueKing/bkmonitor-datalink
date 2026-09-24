// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.serde;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import org.apache.flink.api.common.serialization.SerializationSchema;

/**
 * 把 {@link RumEvent} 序列化成 JSON 字节,用于将迟到事件写回 Kafka({@code rum-events-late})。 序列化失败时抛 {@link
 * IllegalArgumentException},由 sink 的投递语义决定是否重试。
 */
public class RumEventSerializationSchema implements SerializationSchema<RumEvent> {
  private static final long serialVersionUID = 1L;

  // ObjectMapper 不可序列化,用 transient + 懒加载。
  private transient ObjectMapper objectMapper;

  @Override
  public byte[] serialize(RumEvent element) {
    try {
      return mapper().writeValueAsBytes(element);
    } catch (IOException e) {
      throw new IllegalArgumentException("Failed to serialize RUM event", e);
    }
  }

  private ObjectMapper mapper() {
    if (objectMapper == null) {
      objectMapper = new ObjectMapper();
    }
    return objectMapper;
  }
}
