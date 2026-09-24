// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import java.io.IOException;
import org.apache.flink.api.common.serialization.SerializerConfigImpl;
import org.apache.flink.api.common.typeinfo.TypeInformation;
import org.apache.flink.api.common.typeutils.TypeSerializer;
import org.apache.flink.core.memory.DataOutputSerializer;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * 用 Flink 自带的 TypeSerializer 估算 keyed 状态的大小。
 *
 * <p>序列化出的字节数与 RocksDB 实际落盘大小不同(后者还包含 key 前缀、SST 压缩等), 但对「单 key
 * 状态是否随事件累积而膨胀」是一个零依赖的可靠近似, 便于在日志里观察状态大小趋势。
 */
public final class StateSizeEstimator {

  private static final Logger LOG = LoggerFactory.getLogger(StateSizeEstimator.class);

  /** 初始 buffer 大小:大多数聚合对象远小于此值,buffer 无需扩容。 */
  private static final int INITIAL_BUFFER_BYTES = 1 << 16;

  /** 序列化失败时的返回值,避免把异常带进业务链路。 */
  private static final long SERIALIZE_FAILED_BYTES = -1L;

  /** 仅提供单对象序列化大小估算；不在 JVM 内复制活跃 key。 */
  private StateSizeEstimator() {}

  /**
   * 用 Flink 的类型序列化器估算对象序列化后的字节数。
   *
   * @param obj 需要估算的状态对象
   * @return 序列化字节数;对象为 null 时返回 0,序列化失败时返回 {@link #SERIALIZE_FAILED_BYTES}
   */
  public static long serializedSizeBytes(Object obj) {
    if (obj == null) {
      return 0L;
    }
    TypeInformation<?> typeInfo = TypeInformation.of(obj.getClass());
    TypeSerializer<Object> serializer = serializer(typeInfo);
    if (serializer == null) {
      return SERIALIZE_FAILED_BYTES;
    }
    DataOutputSerializer out = new DataOutputSerializer(INITIAL_BUFFER_BYTES);
    try {
      serializer.serialize(obj, out);
      return out.length();
    } catch (IOException e) {
      LOG.warn("failed to estimate state size for type {}", obj.getClass().getName(), e);
      return SERIALIZE_FAILED_BYTES;
    }
  }

  // TypeInformation exposes a serializer with an erased generic type at this API boundary.
  @SuppressWarnings("unchecked")
  private static TypeSerializer<Object> serializer(TypeInformation<?> typeInfo) {
    try {
      return (TypeSerializer<Object>) typeInfo.createSerializer(new SerializerConfigImpl());
    } catch (Exception e) {
      LOG.warn("failed to create serializer for type {}", typeInfo.getTypeClass(), e);
      return null;
    }
  }
}
