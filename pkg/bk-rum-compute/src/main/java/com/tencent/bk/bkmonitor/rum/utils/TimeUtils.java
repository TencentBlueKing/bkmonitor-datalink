// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;

/**
 * 处理时间算术与日期格式化的共享工具。
 *
 * <p>所有方法均为无状态静态调用，专供算子内做 timer 调度与日志时间转换。
 */
public final class TimeUtils {

  /** 毫秒转微秒的乘法因子。 */
  public static final long MS_TO_US = 1_000L;

  /** 微秒转毫秒的除法因子。 */
  public static final long US_TO_MS = 1_000L;

  /** 纳秒转微秒的除法因子。 */
  public static final long NANO_TO_US = 1_000L;

  /** 秒转毫秒的乘法因子。 */
  public static final long SECOND_TO_MS = 1_000L;

  /** 分钟转毫秒的乘法因子。 */
  public static final long MINUTE_TO_MS = 60L * SECOND_TO_MS;

  /** 秒转微秒的乘法因子。 */
  public static final long SECOND_TO_US = SECOND_TO_MS * MS_TO_US;

  /** 在 1 分钟基础上多留 buffer，覆盖 checkpoint 与 timer 调度开销。 */
  private static final long STATE_TTL_BUFFER_MS = Duration.ofMinutes(1).toMillis();

  private TimeUtils() {}

  /**
   * 饱和加法，避免极端时间戳溢出。
   *
   * @param timestamp 基准时间（ms）
   * @param duration 增量（ms），小于等于 0 时直接返回 {@code timestamp}
   * @return {@code timestamp + duration}，溢出时回退为 {@link Long#MAX_VALUE}
   */
  public static long saturatingAdd(long timestamp, long duration) {
    if (duration <= 0L || timestamp >= Long.MAX_VALUE - duration) {
      return Long.MAX_VALUE;
    }
    return timestamp + duration;
  }

  /**
   * 推导窗口状态的默认 TTL：取 gap / max-life 的较大值，再加 1 分钟 buffer。
   *
   * @param gapMs gap 窗口（ms）
   * @param maxLifeMs 最长生命周期（ms）
   * @return 推导出的 state TTL（ms）；窗口接近 {@link Long#MAX_VALUE} 时返回 {@link Long#MAX_VALUE}
   */
  public static long defaultStateTtlMs(long gapMs, long maxLifeMs) {
    long window = Math.max(gapMs, maxLifeMs);
    return window >= Long.MAX_VALUE - STATE_TTL_BUFFER_MS ? Long.MAX_VALUE : window + STATE_TTL_BUFFER_MS;
  }

  /** 微秒 epoch 转 UTC 日期字符串（yyyy-MM-dd），用于 ES 日索引与按天筛选。 */
  public static String toUtcDate(long timestampUs) {
    return Instant.ofEpochMilli(Math.floorDiv(timestampUs, US_TO_MS))
        .atZone(ZoneOffset.UTC)
        .toLocalDate()
        .toString();
  }
}
