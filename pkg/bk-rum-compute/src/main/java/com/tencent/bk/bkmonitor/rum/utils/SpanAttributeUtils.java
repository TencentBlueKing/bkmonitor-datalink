// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import java.util.Locale;
import java.util.Map;
import javax.annotation.Nullable;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/** Span属性安全提取工具 */
public final class SpanAttributeUtils {

  private static final Logger LOG = LoggerFactory.getLogger(SpanAttributeUtils.class);

  private SpanAttributeUtils() {}

  /**
   * 从Span的属性中安全获取字符串值
   *
   * @param attributes Span的属性Map
   * @param key 属性key
   * @return 属性值，如果不存在或为null则返回null
   */
  @Nullable
  public static String getAttributeString(
      @Nullable Map<String, Object> attributes, @Nullable String key) {
    if (attributes == null || key == null) {
      return null;
    }

    Object value = attributes.get(key);
    if (value == null) {
      return null;
    }

    return value.toString();
  }

  /**
   * 从Span的属性中安全获取long
   *
   * @param attributes Span的属性Map
   * @param key 属性key
   * @return 属性值，如果不存在或为null则返回null
   */
  @Nullable
  public static Long getAttributeLong(
      @Nullable Map<String, Object> attributes, @Nullable String key) {
    if (attributes == null || key == null) {
      return null;
    }

    Object value = attributes.get(key);
    if (value == null) {
      return null;
    }

    try {
      if (value instanceof Long) {
        return (Long) value;
      } else if (value instanceof Number) {
        return ((Number) value).longValue();
      } else {
        return Long.parseLong(value.toString());
      }
    } catch (NumberFormatException e) {
      return null;
    }
  }

  /**
   * 从Span的属性中安全获取字符串值（带默认值）
   *
   * @param attributes Span的属性Map
   * @param key 属性key
   * @param defaultValue 默认值
   * @return 属性值，如果不存在或为null则返回默认值
   */
  @Nullable
  public static String getAttributeStringOrDefault(
      @Nullable Map<String, Object> attributes,
      @Nullable String key,
      @Nullable String defaultValue) {
    String value = getAttributeString(attributes, key);
    return value != null ? value : defaultValue;
  }

  /**
   * 从Span的属性中安全获取字符串值，并自动去除前后空格
   *
   * @param attributes Span的属性Map
   * @param key 属性key
   * @return 去除空格后的属性值，如果不存在或为null则返回null
   */
  @Nullable
  public static String getAttributeStringTrimmed(
      @Nullable Map<String, Object> attributes, @Nullable String key) {
    String value = getAttributeString(attributes, key);
    return value != null ? value.trim() : null;
  }

  /**
   * 检查Span的属性中是否存在某个key且值不为空
   *
   * @param attributes Span的属性Map
   * @param key 属性key
   * @return true表示存在且值不为空
   */
  public static boolean hasAttribute(
      @Nullable Map<String, Object> attributes, @Nullable String key) {
    String value = getAttributeString(attributes, key);
    return value != null && !value.trim().isEmpty();
  }

  /**
   * 将 span 键归一化为小写、下划线分隔的形式（{@code -} 替换为 {@code _}），用于跨输入源的 {@code
   * span_type} / {@code span_name} 比较。
   */
  public static String normalizeKey(@Nullable String value) {
    return value == null ? "" : value.trim().toLowerCase(Locale.ROOT).replace('-', '_');
  }

  /**
   * 记录警告日志（当属性缺失时）
   *
   * @param attributes Span的属性Map
   * @param key 属性key
   * @param defaultValue 默认值
   * @return 属性值或默认值
   */
  @Nullable
  public static String getAttributeWithWarning(
      @Nullable Map<String, Object> attributes,
      @Nullable String key,
      @Nullable String defaultValue) {
    String value = getAttributeString(attributes, key);
    if (value == null) {
      LOG.warn("Span attribute '{}' is missing, using default value: '{}'", key, defaultValue);
      return defaultValue;
    }
    return value;
  }
}
