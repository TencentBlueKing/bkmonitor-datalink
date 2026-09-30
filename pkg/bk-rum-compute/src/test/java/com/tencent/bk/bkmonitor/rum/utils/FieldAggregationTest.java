// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.within;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.utils.FieldAggregation.FieldRule;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

/**
 * {@link FieldAggregation} 原子规则单元测试。
 *
 * <p>每个规则在最小 Holder 上验证语义;{@link ApplyAllTest} 验证批量执行的顺序。
 * source 用 {@code Function<RumEvent, V>} 但实现忽略 event 参数直接返回常量,便于纯逻辑断言。
 */
class FieldAggregationTest {

  /** 通用 holder:覆盖字符串/包装 Long/包装 Double 等字段类型。 */
  static final class Holder {
    String s;
    Long lo;
    Long nonNeg;
    Long max;
    Double maxD;
    Long maxL;

    String getS() { return s; }
    void setS(String v) { this.s = v; }
    Long getLo() { return lo; }
    void setLo(Long v) { this.lo = v; }
    Long getNonNeg() { return nonNeg; }
    void setNonNeg(Long v) { this.nonNeg = v; }
    Long getMax() { return max; }
    void setMax(Long v) { this.max = v; }
    Double getMaxD() { return maxD; }
    void setMaxD(Double v) { this.maxD = v; }
    Long getMaxL() { return maxL; }
    void setMaxL(Long v) { this.maxL = v; }
  }

  @Nested
  class FirstNonEmptyTest {

    @Test
    void writesNonEmptyCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule = FieldAggregation.firstNonEmpty(e -> "phase1", Holder::getS, Holder::setS);
      rule.apply(h, null);
      assertThat(h.s).isEqualTo("phase1");
    }

    @Test
    void ignoresNullCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule = FieldAggregation.firstNonEmpty(e -> null, Holder::getS, Holder::setS);
      rule.apply(h, null);
      assertThat(h.s).isNull();
    }

    @Test
    void ignoresBlankCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule = FieldAggregation.firstNonEmpty(e -> "   ", Holder::getS, Holder::setS);
      rule.apply(h, null);
      assertThat(h.s).isNull();
    }

    @Test
    void doesNotOverwriteExistingValue() {
      Holder h = new Holder();
      h.s = "phase1";
      FieldRule<Holder> rule = FieldAggregation.firstNonEmpty(e -> "phase2", Holder::getS, Holder::setS);
      rule.apply(h, null);
      assertThat(h.s).isEqualTo("phase1");
    }

    @Test
    void doesNotTrimCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule =
          FieldAggregation.firstNonEmpty(e -> "  hello  ", Holder::getS, Holder::setS);
      rule.apply(h, null);
      assertThat(h.s).isEqualTo("  hello  ");
    }

    @Test
    void doesNotEvaluateSourceWhenValueAlreadyExists() {
      Holder h = new Holder();
      h.s = "phase1";
      AtomicInteger evaluations = new AtomicInteger();
      FieldRule<Holder> rule =
          FieldAggregation.firstNonEmpty(
              event -> {
                evaluations.incrementAndGet();
                return "phase2";
              },
              Holder::getS,
              Holder::setS);

      rule.apply(h, new RumEvent());

      assertThat(evaluations).hasValue(0);
      assertThat(h.s).isEqualTo("phase1");
    }
  }

  @Nested
  class FirstNonNullBoxedTest {

    @Test
    void writesFirstNonNullCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule = FieldAggregation.firstNonNullBoxed(e -> 42L, Holder::getLo, Holder::setLo);
      rule.apply(h, null);
      assertThat(h.lo).isEqualTo(42L);
    }

    @Test
    void ignoresNullCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule = FieldAggregation.firstNonNullBoxed(e -> null, Holder::getLo, Holder::setLo);
      rule.apply(h, null);
      assertThat(h.lo).isNull();
    }

    @Test
    void doesNotOverwriteExistingValue() {
      Holder h = new Holder();
      h.lo = 1L;
      FieldRule<Holder> rule =
          FieldAggregation.firstNonNullBoxed(e -> 2L, Holder::getLo, Holder::setLo);
      rule.apply(h, null);
      assertThat(h.lo).isEqualTo(1L);
    }

    @Test
    void doesNotEvaluateSourceWhenValueAlreadyExists() {
      Holder h = new Holder();
      h.lo = 1L;
      AtomicInteger evaluations = new AtomicInteger();
      FieldRule<Holder> rule =
          FieldAggregation.firstNonNullBoxed(
              event -> {
                evaluations.incrementAndGet();
                return 2L;
              },
              Holder::getLo,
              Holder::setLo);

      rule.apply(h, new RumEvent());

      assertThat(evaluations).hasValue(0);
      assertThat(h.lo).isEqualTo(1L);
    }
  }

  @Nested
  class FirstNonNegativeLongTest {

    @Test
    void writesZeroCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule =
          FieldAggregation.firstNonNegativeLong(e -> 0L, Holder::getNonNeg, Holder::setNonNeg);
      rule.apply(h, null);
      assertThat(h.nonNeg).isEqualTo(0L);
    }

    @Test
    void writesPositiveCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule =
          FieldAggregation.firstNonNegativeLong(e -> 123L, Holder::getNonNeg, Holder::setNonNeg);
      rule.apply(h, null);
      assertThat(h.nonNeg).isEqualTo(123L);
    }

    @Test
    void ignoresNegativeCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule =
          FieldAggregation.firstNonNegativeLong(e -> -1L, Holder::getNonNeg, Holder::setNonNeg);
      rule.apply(h, null);
      assertThat(h.nonNeg).isNull();
    }

    @Test
    void ignoresNullCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule =
          FieldAggregation.firstNonNegativeLong(e -> null, Holder::getNonNeg, Holder::setNonNeg);
      rule.apply(h, null);
      assertThat(h.nonNeg).isNull();
    }

    @Test
    void doesNotOverwriteExistingValue() {
      Holder h = new Holder();
      h.nonNeg = 5L;
      FieldRule<Holder> rule =
          FieldAggregation.firstNonNegativeLong(
              e -> 99L, Holder::getNonNeg, Holder::setNonNeg);
      rule.apply(h, null);
      assertThat(h.nonNeg).isEqualTo(5L);
    }

    @Test
    void doesNotEvaluateSourceWhenValueAlreadyExists() {
      Holder h = new Holder();
      h.nonNeg = 5L;
      AtomicInteger evaluations = new AtomicInteger();
      FieldRule<Holder> rule =
          FieldAggregation.firstNonNegativeLong(
              event -> {
                evaluations.incrementAndGet();
                return 99L;
              },
              Holder::getNonNeg,
              Holder::setNonNeg);

      rule.apply(h, new RumEvent());

      assertThat(evaluations).hasValue(0);
      assertThat(h.nonNeg).isEqualTo(5L);
    }
  }

  @Nested
  class MaxLongTest {

    @Test
    void writesFirstNonNullCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule = FieldAggregation.maxLong(e -> 10L, Holder::getMax, Holder::setMax);
      rule.apply(h, null);
      assertThat(h.max).isEqualTo(10L);
    }

    @Test
    void keepsLargerValue() {
      Holder h = new Holder();
      h.max = 50L;
      FieldRule<Holder> rule = FieldAggregation.maxLong(e -> 10L, Holder::getMax, Holder::setMax);
      rule.apply(h, null);
      assertThat(h.max).isEqualTo(50L);
    }

    @Test
    void overwritesWithLargerValue() {
      Holder h = new Holder();
      h.max = 5L;
      FieldRule<Holder> rule = FieldAggregation.maxLong(e -> 10L, Holder::getMax, Holder::setMax);
      rule.apply(h, null);
      assertThat(h.max).isEqualTo(10L);
    }

    @Test
    void ignoresNullCandidate() {
      Holder h = new Holder();
      h.max = 7L;
      FieldRule<Holder> rule = FieldAggregation.maxLong(e -> null, Holder::getMax, Holder::setMax);
      rule.apply(h, null);
      assertThat(h.max).isEqualTo(7L);
    }
  }

  @Nested
  class MaxDoubleTest {

    @Test
    void writesFirstCandidate() {
      Holder h = new Holder();
      FieldRule<Holder> rule = FieldAggregation.maxDouble(e -> 0.1, Holder::getMaxD, Holder::setMaxD);
      rule.apply(h, null);
      assertThat(h.maxD).isCloseTo(0.1, within(1e-9));
    }

    @Test
    void keepsLargerValue() {
      Holder h = new Holder();
      h.maxD = 0.5;
      FieldRule<Holder> rule = FieldAggregation.maxDouble(e -> 0.1, Holder::getMaxD, Holder::setMaxD);
      rule.apply(h, null);
      assertThat(h.maxD).isCloseTo(0.5, within(1e-9));
    }

    @Test
    void overwritesWithLargerValue() {
      Holder h = new Holder();
      h.maxD = 0.1;
      FieldRule<Holder> rule = FieldAggregation.maxDouble(e -> 0.5, Holder::getMaxD, Holder::setMaxD);
      rule.apply(h, null);
      assertThat(h.maxD).isCloseTo(0.5, within(1e-9));
    }

    @Test
    void ignoresNullCandidate() {
      Holder h = new Holder();
      h.maxD = 0.3;
      FieldRule<Holder> rule = FieldAggregation.maxDouble(e -> null, Holder::getMaxD, Holder::setMaxD);
      rule.apply(h, null);
      assertThat(h.maxD).isCloseTo(0.3, within(1e-9));
    }
  }

  @Nested
  class MaxDoubleAsLongTest {

    @Test
    void roundsAndWrites() {
      Holder h = new Holder();
      FieldRule<Holder> rule =
          FieldAggregation.maxDoubleAsLong(e -> 1.4, Holder::getMaxL, Holder::setMaxL);
      rule.apply(h, null);
      assertThat(h.maxL).isEqualTo(1L);
    }

    @Test
    void roundsHalfUp() {
      Holder h = new Holder();
      FieldRule<Holder> rule =
          FieldAggregation.maxDoubleAsLong(e -> 1.5, Holder::getMaxL, Holder::setMaxL);
      rule.apply(h, null);
      assertThat(h.maxL).isEqualTo(2L);
    }

    @Test
    void takesMaxWithRounded() {
      Holder h = new Holder();
      h.maxL = 5L;
      FieldRule<Holder> rule =
          FieldAggregation.maxDoubleAsLong(e -> 3.4, Holder::getMaxL, Holder::setMaxL);
      rule.apply(h, null);
      assertThat(h.maxL).isEqualTo(5L);

      FieldRule<Holder> rule2 =
          FieldAggregation.maxDoubleAsLong(e -> 9.4, Holder::getMaxL, Holder::setMaxL);
      rule2.apply(h, null);
      assertThat(h.maxL).isEqualTo(9L);
    }

    @Test
    void ignoresNullCandidate() {
      Holder h = new Holder();
      h.maxL = 4L;
      FieldRule<Holder> rule =
          FieldAggregation.maxDoubleAsLong(e -> null, Holder::getMaxL, Holder::setMaxL);
      rule.apply(h, null);
      assertThat(h.maxL).isEqualTo(4L);
    }
  }

  @Nested
  class ApplyAllTest {

    @Test
    void appliesRulesInOrder() {
      Holder h = new Holder();
      List<FieldRule<Holder>> rules =
          List.of(
              FieldAggregation.firstNonEmpty(e -> "first", Holder::getS, Holder::setS),
              FieldAggregation.firstNonNullBoxed(e -> 100L, Holder::getLo, Holder::setLo),
              FieldAggregation.maxLong(e -> 10L, Holder::getMax, Holder::setMax));
      FieldAggregation.applyAll(rules, h, null);
      assertThat(h.s).isEqualTo("first");
      assertThat(h.lo).isEqualTo(100L);
      assertThat(h.max).isEqualTo(10L);
    }

    @Test
    void noRulesIsNoop() {
      Holder h = new Holder();
      FieldAggregation.applyAll(List.of(), h, null);
      assertThat(h.s).isNull();
      assertThat(h.lo).isNull();
      assertThat(h.max).isNull();
    }
  }
}
