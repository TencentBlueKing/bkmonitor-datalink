// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import com.tencent.bk.bkmonitor.rum.model.Resource;
import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import java.util.List;
import java.util.Map;
import java.util.function.Function;
import javax.annotation.Nullable;

/**
 * 把分散在累加器 / Process 函数中的「字段规则」收敛成可复用的原子规则。
 *
 * <p>每条事件到来时,累加器按声明顺序遍历 {@link FieldRule} 列表,每条规则把 RumEvent 中的候选值
 * 投影到目标字段。所有规则按 O(1) 设计,调用时机与频次与抽象前一致(每条事件一次),不改变
 * timer / State TTL / emit 节奏 / 事件时间字段语义。
 *
 * <p>典型用法：
 * <pre>{@code
 * private static final List<FieldAggregation.FieldRule<MyDoc>> FIELD_RULES = List.of(
 *     FieldAggregation.firstNonEmpty(
 *         (event, resource, attributes) -> SpanAttributeUtils.getAttributeStringTrimmed(
 *                 attributes, "view.phase"),
 *         MyDoc::getPhase, MyDoc::setPhase),
 *     FieldAggregation.firstNonNegativeLong(
 *         (event, resource, attributes) ->
 *             SpanAttributeUtils.getAttributeLong(attributes, "view.loading_time"),
 *         MyDoc::getViewLoadingTime, MyDoc::setViewLoadingTime),
 *     FieldAggregation.maxLong(
 *         (event, resource, attributes) -> readLongAttr(attributes, "vital.lcp"),
 *         MyDoc::getLcp, MyDoc::setLcp)
 * );
 *
 * public void add(RumEvent event) {
 *     Map<String, Object> attributes = attributesOf(event);
 *     Resource resource = resourceOf(event);
 *     FieldAggregation.applyAll(FIELD_RULES, this, event, resource, attributes);
 * }
 * }</pre>
 */
public final class FieldAggregation {

  private FieldAggregation() {}

  /** target 上的字段读取。 */
  @FunctionalInterface
  public interface Getter<T, V> {
    V get(T target);
  }

  /** target 上的字段写入。 */
  @FunctionalInterface
  public interface Setter<T, V> {
    void set(T target, V value);
  }

  /** 字段候选值来源。一次事件处理会复用已提取的嵌套对象。 */
  @FunctionalInterface
  public interface FieldSource<V> {
    V get(
        RumEvent event,
        @Nullable Resource resource,
        @Nullable Map<String, Object> attributes);
  }

  /** 一条字段规则：从 RumEvent 取候选值,决定是否更新 target。 */
  @FunctionalInterface
  public interface FieldRule<T> {
    void apply(
        T target,
        RumEvent event,
        @Nullable Resource resource,
        @Nullable Map<String, Object> attributes);

    /** 使用原有调用方式执行规则；嵌套对象由规则自行获取。 */
    default void apply(T target, RumEvent event) {
      apply(target, event, null, null);
    }
  }

  public static <T> FieldRule<T> firstNonEmpty(
      Function<RumEvent, String> source,
      Getter<T, String> getter,
      Setter<T, String> setter) {
    return firstNonEmpty(
        (event, resource, attributes) -> source.apply(event), getter, setter);
  }

  /**
   * 字符串字段「首次非空保留」。
   *
   * <p>candidate 为 null 或 trim 后为空时不写;target 当前值非空时也不写(保留首次有效样本)。
   * target 已有值时不会再计算 candidate，避免重复解析输入。candidate 不做 trim,以便与 {@code
   * SpanAttributeUtils.getAttributeStringTrimmed} 等已 trim 过的候选值组合时保留调用方的语义。
   */
  public static <T> FieldRule<T> firstNonEmpty(
      FieldSource<String> source, Getter<T, String> getter, Setter<T, String> setter) {
    return (target, event, resource, attributes) -> {
      String current = getter.get(target);
      if (current != null && !current.isEmpty()) {
        return;
      }
      String candidate = source.get(event, resource, attributes);
      if (candidate == null || candidate.trim().isEmpty()) {
        return;
      }
      setter.set(target, candidate);
    };
  }

  public static <T, V> FieldRule<T> firstNonNullBoxed(
      Function<RumEvent, V> source, Getter<T, V> getter, Setter<T, V> setter) {
    return firstNonNullBoxed(
        (event, resource, attributes) -> source.apply(event), getter, setter);
  }

  /**
   * 任意包装类型字段「首次非 null 保留」。
   *
   * <p>candidate 为 null 不写;candidate 非 null 且 target 当前为 null 时写入。target 已有值时不会再计算
   * candidate。适合版本号、bkBizId(Integer)、traceId(String 之外的)等纯标识类字段。
   */
  public static <T, V> FieldRule<T> firstNonNullBoxed(
      FieldSource<V> source, Getter<T, V> getter, Setter<T, V> setter) {
    return (target, event, resource, attributes) -> {
      if (getter.get(target) != null) {
        return;
      }
      V candidate = source.get(event, resource, attributes);
      if (candidate == null) {
        return;
      }
      setter.set(target, candidate);
    };
  }

  public static <T> FieldRule<T> firstNonNegativeLong(
      Function<RumEvent, Long> source, Getter<T, Long> getter, Setter<T, Long> setter) {
    return firstNonNegativeLong(
        (event, resource, attributes) -> source.apply(event), getter, setter);
  }

  /**
   * 包装 Long 字段「首次非负保留」。
   *
   * <p>candidate 为 null 或小于 0 时不写;candidate {@code >= 0} 且 target 当前为 null 时写入。target
   * 已有值时不会再计算 candidate。适合 loading_time / started_at 这类"取值应当 >= 0"的耗时或时间戳字段。
   */
  public static <T> FieldRule<T> firstNonNegativeLong(
      FieldSource<Long> source, Getter<T, Long> getter, Setter<T, Long> setter) {
    return (target, event, resource, attributes) -> {
      if (getter.get(target) != null) {
        return;
      }
      Long candidate = source.get(event, resource, attributes);
      if (candidate == null || candidate < 0L) {
        return;
      }
      setter.set(target, candidate);
    };
  }

  public static <T> FieldRule<T> maxLong(
      Function<RumEvent, Long> source, Getter<T, Long> getter, Setter<T, Long> setter) {
    return maxLong((event, resource, attributes) -> source.apply(event), getter, setter);
  }

  /**
   * 包装 Long 字段「取最大」。
   *
   * <p>candidate 为 null 时不写;target 当前为 null 时直接写入 candidate,否则取
   * {@code Math.max(current, candidate)}。适合 Web Vitals 中的 lcp / inp 等。
   */
  public static <T> FieldRule<T> maxLong(
      FieldSource<Long> source, Getter<T, Long> getter, Setter<T, Long> setter) {
    return (target, event, resource, attributes) -> {
      Long candidate = source.get(event, resource, attributes);
      if (candidate == null) {
        return;
      }
      Long current = getter.get(target);
      setter.set(target, current == null ? candidate : Math.max(current, candidate));
    };
  }

  public static <T> FieldRule<T> maxDouble(
      Function<RumEvent, Double> source, Getter<T, Double> getter, Setter<T, Double> setter) {
    return maxDouble((event, resource, attributes) -> source.apply(event), getter, setter);
  }

  /**
   * 包装 Double 字段「取最大」。
   *
   * <p>candidate 为 null 时不写;target 当前为 null 时直接写入 candidate,否则取
   * {@code Math.max(current, candidate)}。适合 Web Vitals 中的 cls。
   */
  public static <T> FieldRule<T> maxDouble(
      FieldSource<Double> source, Getter<T, Double> getter, Setter<T, Double> setter) {
    return (target, event, resource, attributes) -> {
      Double candidate = source.get(event, resource, attributes);
      if (candidate == null) {
        return;
      }
      Double current = getter.get(target);
      setter.set(target, current == null ? candidate : Math.max(current, candidate));
    };
  }

  public static <T> FieldRule<T> maxDoubleAsLong(
      Function<RumEvent, Double> source, Getter<T, Long> getter, Setter<T, Long> setter) {
    return maxDoubleAsLong((event, resource, attributes) -> source.apply(event), getter, setter);
  }

  /**
   * Double 候选值四舍五入到 Long 后取最大。
   *
   * <p>candidate 为 null 时不写;否则 {@code Math.round(candidate)} 与 target 当前值取 max。
   * 适合 inp/lcp 这类以毫秒整数表达、但埋点可能上送浮点的指标。
   */
  public static <T> FieldRule<T> maxDoubleAsLong(
      FieldSource<Double> source, Getter<T, Long> getter, Setter<T, Long> setter) {
    return (target, event, resource, attributes) -> {
      Double candidate = source.get(event, resource, attributes);
      if (candidate == null) {
        return;
      }
      long rounded = Math.round(candidate);
      Long current = getter.get(target);
      setter.set(target, current == null ? rounded : Math.max(current, rounded));
    };
  }

  /** 按声明顺序执行规则，兼容不需要预提取嵌套对象的调用方。 */
  public static <T> void applyAll(List<FieldRule<T>> rules, T target, RumEvent event) {
    applyAll(rules, target, event, null, null);
  }

  /**
   * 按声明顺序对 target 批量应用规则列表。每条规则共享当前事件的嵌套对象引用。
   */
  public static <T> void applyAll(
      List<FieldRule<T>> rules,
      T target,
      RumEvent event,
      @Nullable Resource resource,
      @Nullable Map<String, Object> attributes) {
    for (FieldRule<T> rule : rules) {
      rule.apply(target, event, resource, attributes);
    }
  }
}