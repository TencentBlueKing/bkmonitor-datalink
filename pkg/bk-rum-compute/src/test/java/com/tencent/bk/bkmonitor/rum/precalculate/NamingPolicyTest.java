// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.precalculate;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import java.time.LocalDate;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

/**
 * {@link NamingPolicy} 单元测试。
 *
 * <p>覆盖四块：① 常量契约；② view / session 索引名生成；③ 路由 / 节点 key 生成；④ 输入校验。{@link
 * CrossFamilyContractTest} 是与 BK-Monitor Python 端的「绝对契约」——修改任何字符串拼接规则前必须同步 Python 端，
 * 并更新该测试。
 */
class NamingPolicyTest {

  // ────────────────────────── 常量契约 ──────────────────────────

  @Nested
  class ConstantsTest {

    @Test
    void dateFormatIsYyyyMMdd() {
      assertThat(NamingPolicy.DATE_FORMAT).isEqualTo("yyyyMMdd");
    }

    @Test
    void writePrefixIsWriteUnderscore() {
      assertThat(NamingPolicy.WRITE_PREFIX).isEqualTo("write_");
    }

    @Test
    void writeDateSeparatorIsUnderscore() {
      assertThat(NamingPolicy.WRITE_DATE_SEPARATOR).isEqualTo("_");
    }

    @Test
    void nodeKeySeparatorIsDash() {
      assertThat(NamingPolicy.NODE_KEY_SEPARATOR).isEqualTo("-");
    }

    @Test
    void routeKeySeparatorIsColon() {
      assertThat(NamingPolicy.ROUTE_KEY_SEPARATOR).isEqualTo(":");
    }
  }

  // ────────────────────────── VIEW 家族 ──────────────────────────
  //
  // 对应 BK-Monitor Python 端：
  //   TABLE_PREFIX = "rum_global.precalculate"
  //   kind = "view"
  //   → "rum_global_view.precalculate_auto_{N}"

  @Nested
  class ViewFamilyTest {

    @Test
    void logicalTableFormat() {
      assertThat(
          NamingPolicy.logicalTable(PrecalculateFamily.VIEW, 1))
          .isEqualTo("rum_global_view.precalculate_auto_1");
      assertThat(
          NamingPolicy.logicalTable(PrecalculateFamily.VIEW, 5))
          .isEqualTo("rum_global_view.precalculate_auto_5");
    }

    @Test
    void indexSetReplacesDot() {
      assertThat(
          NamingPolicy.indexSet(PrecalculateFamily.VIEW, 3))
          .isEqualTo("rum_global_view_precalculate_auto_3");
    }

    @Test
    void writeIndexForView() {
      assertThat(
          NamingPolicy.writeIndex(PrecalculateFamily.VIEW, 2, LocalDate.of(2026, 9, 10)))
          .isEqualTo("write_20260910_rum_global_view_precalculate_auto_2");
    }

    @Test
    void searchPatternForView() {
      assertThat(
          NamingPolicy.searchPattern(PrecalculateFamily.VIEW, 2))
          .isEqualTo("rum_global_view_precalculate_auto_2*");
    }

    @Test
    void viewBusinessPrefixIsRumGlobalView() {
      assertThat(PrecalculateFamily.VIEW.businessPrefix()).isEqualTo("rum_global_view.");
    }

    @Test
    void viewShard0Throws() {
      assertThatThrownBy(
          () -> NamingPolicy.logicalTable(PrecalculateFamily.VIEW, 0))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void namingSupportsShardCountsAboveDefault() {
      assertThat(NamingPolicy.logicalTable(PrecalculateFamily.VIEW, 6))
          .isEqualTo("rum_global_view.precalculate_auto_6");
    }
  }

  // ────────────────────────── SESSION 家族 ───────────────────────
  //
  // 对应 BK-Monitor Python 端：
  //   TABLE_PREFIX = "rum_global.precalculate"
  //   kind = "session"
  //   → "rum_global_session.precalculate_auto_{N}"

  @Nested
  class SessionFamilyTest {

    @Test
    void logicalTableFormat() {
      assertThat(
          NamingPolicy.logicalTable(PrecalculateFamily.SESSION, 1))
          .isEqualTo("rum_global_session.precalculate_auto_1");
      assertThat(
          NamingPolicy.logicalTable(PrecalculateFamily.SESSION, 5))
          .isEqualTo("rum_global_session.precalculate_auto_5");
    }

    @Test
    void indexSetReplacesDot() {
      assertThat(
          NamingPolicy.indexSet(PrecalculateFamily.SESSION, 4))
          .isEqualTo("rum_global_session_precalculate_auto_4");
    }

    @Test
    void writeIndexForSession() {
      assertThat(
          NamingPolicy.writeIndex(PrecalculateFamily.SESSION, 1, LocalDate.of(2026, 1, 1)))
          .isEqualTo("write_20260101_rum_global_session_precalculate_auto_1");
    }

    @Test
    void searchPatternForSession() {
      assertThat(
          NamingPolicy.searchPattern(PrecalculateFamily.SESSION, 5))
          .isEqualTo("rum_global_session_precalculate_auto_5*");
    }

    @Test
    void sessionBusinessPrefixIsRumGlobalSession() {
      assertThat(PrecalculateFamily.SESSION.businessPrefix()).isEqualTo("rum_global_session.");
    }

    @Test
    void sessionShard0Throws() {
      assertThatThrownBy(
          () -> NamingPolicy.logicalTable(PrecalculateFamily.SESSION, 0))
          .isInstanceOf(IllegalArgumentException.class);
    }
  }

  // ────────────────────────── 路由 key ───────────────────────────

  @Nested
  class RouteKeyTest {

    @Test
    void appRouteKeyFormat() {
      assertThat(NamingPolicy.routeKey(123L, "myApp")).isEqualTo("123:myApp");
    }

    @Test
    void appRouteKeyAcceptsZeroBizId() {
      assertThat(NamingPolicy.routeKey(0L, "app")).isEqualTo("0:app");
    }

    @Test
    void appRouteKeyRejectsNullAppName() {
      assertThatThrownBy(
          () -> NamingPolicy.routeKey(1L, null))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void appRouteKeyRejectsEmptyAppName() {
      assertThatThrownBy(
          () -> NamingPolicy.routeKey(1L, ""))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void appRouteKeyRejectsBlankAppName() {
      assertThatThrownBy(
          () -> NamingPolicy.routeKey(1L, "  "))
          .isInstanceOf(IllegalArgumentException.class);
    }
  }

  // ────────────────────────── 节点 key ───────────────────────────

  @Nested
  class NodeKeyTest {

    @Test
    void formatIsClusterIdDashTableName() {
      assertThat(
          NamingPolicy.nodeKey(3L, "rum_global_view.precalculate_auto_1"))
          .isEqualTo("3-rum_global_view.precalculate_auto_1");
    }

    @Test
    void acceptsZeroClusterId() {
      assertThat(
          NamingPolicy.nodeKey(0L, "rum_global_session.precalculate_auto_1"))
          .isEqualTo("0-rum_global_session.precalculate_auto_1");
    }

    @Test
    void rejectsNullTableName() {
      assertThatThrownBy(
          () -> NamingPolicy.nodeKey(1L, null))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void rejectsEmptyTableName() {
      assertThatThrownBy(
          () -> NamingPolicy.nodeKey(1L, ""))
          .isInstanceOf(IllegalArgumentException.class);
    }
  }

  // ────────────────────────── 跨家族契约 ─────────────────────────
  //
  // 本测试类是 NamingPolicy 与 BK-Monitor Python 端的「绝对契约」。
  // 修改任何字符串拼接规则前必须先：
  //   1. 同步 BK-Monitor Python 端 storage.py 的同名方法
  //   2. 更新本测试的预期值
  //   3. 跑跨语言集成测试确认一致
  //
  // BK-Monitor Python 参考：
  //   get_index_write_alias(index) → "write_{date:%Y%m%d}_{index}"
  //   origin_index_name = result_table_id.replace(".", "_")
  //   search_index_name = origin_index_name + "*"

  @Nested
  class CrossFamilyContractTest {

    @Test
    void viewFullPipelineContract() {
      assertThat(
          NamingPolicy.logicalTable(PrecalculateFamily.VIEW, 1))
          .isEqualTo("rum_global_view.precalculate_auto_1");
      assertThat(
          NamingPolicy.indexSet(PrecalculateFamily.VIEW, 1))
          .isEqualTo("rum_global_view_precalculate_auto_1");
      assertThat(
          NamingPolicy.writeIndex(PrecalculateFamily.VIEW, 1, LocalDate.of(2026, 9, 10)))
          .isEqualTo("write_20260910_rum_global_view_precalculate_auto_1");
      assertThat(
          NamingPolicy.searchPattern(PrecalculateFamily.VIEW, 1))
          .isEqualTo("rum_global_view_precalculate_auto_1*");
    }

    @Test
    void sessionFullPipelineContract() {
      assertThat(
          NamingPolicy.logicalTable(PrecalculateFamily.SESSION, 5))
          .isEqualTo("rum_global_session.precalculate_auto_5");
      assertThat(
          NamingPolicy.indexSet(PrecalculateFamily.SESSION, 5))
          .isEqualTo("rum_global_session_precalculate_auto_5");
      assertThat(
          NamingPolicy.writeIndex(PrecalculateFamily.SESSION, 5, LocalDate.of(2026, 1, 1)))
          .isEqualTo("write_20260101_rum_global_session_precalculate_auto_5");
      assertThat(
          NamingPolicy.searchPattern(PrecalculateFamily.SESSION, 5))
          .isEqualTo("rum_global_session_precalculate_auto_5*");
    }

    @Test
    void allShardsLogicalTableContract() {
      for (int n = 1; n <= PrecalculateStorage.DEFAULT_DISPERSED_COUNT; n++) {
        assertThat(
            NamingPolicy.logicalTable(PrecalculateFamily.VIEW, n))
            .isEqualTo("rum_global_view.precalculate_auto_" + n);
        assertThat(
            NamingPolicy.indexSet(PrecalculateFamily.VIEW, n))
            .isEqualTo("rum_global_view_precalculate_auto_" + n);
        assertThat(
            NamingPolicy.searchPattern(PrecalculateFamily.VIEW, n))
            .isEqualTo("rum_global_view_precalculate_auto_" + n + "*");
      }
      for (int n = 1; n <= PrecalculateStorage.DEFAULT_DISPERSED_COUNT; n++) {
        assertThat(
            NamingPolicy.logicalTable(PrecalculateFamily.SESSION, n))
            .isEqualTo("rum_global_session.precalculate_auto_" + n);
        assertThat(
            NamingPolicy.indexSet(PrecalculateFamily.SESSION, n))
            .isEqualTo("rum_global_session_precalculate_auto_" + n);
        assertThat(
            NamingPolicy.searchPattern(PrecalculateFamily.SESSION, n))
            .isEqualTo("rum_global_session_precalculate_auto_" + n + "*");
      }
    }

    @Test
    void writeIndexDateFormatContract() {
      // 覆盖年初/年末/闰年/普通日期的契约值，BK-Monitor Python 端使用相同 strftime("%Y%m%d") 行为
      assertThat(
          NamingPolicy.writeIndex(PrecalculateFamily.VIEW, 1, LocalDate.of(2026, 1, 1)))
          .isEqualTo("write_20260101_rum_global_view_precalculate_auto_1");
      assertThat(
          NamingPolicy.writeIndex(PrecalculateFamily.SESSION, 4, LocalDate.of(2026, 9, 9)))
          .isEqualTo("write_20260909_rum_global_session_precalculate_auto_4");
      assertThat(
          NamingPolicy.writeIndex(PrecalculateFamily.SESSION, 5, LocalDate.of(2026, 12, 31)))
          .isEqualTo("write_20261231_rum_global_session_precalculate_auto_5");
      assertThat(
          NamingPolicy.writeIndex(PrecalculateFamily.VIEW, 2, LocalDate.of(2024, 2, 29)))
          .isEqualTo("write_20240229_rum_global_view_precalculate_auto_2");
    }
  }

  // ────────────────────────── 家族间契约 ─────────────────────────

  @Nested
  class CrossFamilyDistinctnessTest {

    @Test
    void familiesHaveDistinctBusinessPrefixes() {
      // VIEW 和 SESSION 现在各占独立业务域（rum_global_view / rum_global_session）
      assertThat(PrecalculateFamily.VIEW.businessPrefix()).isEqualTo("rum_global_view.");
      assertThat(PrecalculateFamily.SESSION.businessPrefix()).isEqualTo("rum_global_session.");
      assertThat(
          PrecalculateFamily.SESSION.businessPrefix())
          .isNotEqualTo(PrecalculateFamily.VIEW.businessPrefix());
    }

    @Test
    void familiesAreDistinct() {
      assertThat(
          PrecalculateFamily.SESSION.logicalTablePrefix())
          .isNotEqualTo(PrecalculateFamily.VIEW.logicalTablePrefix());
      assertThat(
          PrecalculateFamily.SESSION.purpose())
          .isNotEqualTo(PrecalculateFamily.VIEW.purpose());
    }

    @Test
    void indexSetPrefixReplacesDot() {
      assertThat(
          PrecalculateFamily.VIEW.indexSetPrefix())
          .isEqualTo("rum_global_view_precalculate_auto_");
      assertThat(
          PrecalculateFamily.SESSION.indexSetPrefix())
          .isEqualTo("rum_global_session_precalculate_auto_");
    }

    @Test
    void logicalTablePrefixFormat() {
      assertThat(
          PrecalculateFamily.VIEW.logicalTablePrefix())
          .isEqualTo("rum_global_view.precalculate_auto_");
      assertThat(
          PrecalculateFamily.SESSION.logicalTablePrefix())
          .isEqualTo("rum_global_session.precalculate_auto_");
    }

    @Test
    void nullFamilyThrows() {
      assertThatThrownBy(
          () -> NamingPolicy.logicalTable(null, 1))
          .isInstanceOf(IllegalArgumentException.class);
      assertThatThrownBy(
          () -> NamingPolicy.indexSet((PrecalculateFamily) null, 1))
          .isInstanceOf(IllegalArgumentException.class);
      assertThatThrownBy(
          () -> NamingPolicy.writeIndex((PrecalculateFamily) null, 1, LocalDate.now()))
          .isInstanceOf(IllegalArgumentException.class);
      assertThatThrownBy(
          () -> NamingPolicy.searchPattern((PrecalculateFamily) null, 1))
          .isInstanceOf(IllegalArgumentException.class);
    }
  }

  // ────────────────────────── 幂等性 ────────────────────────────

  @Nested
  class StabilityTest {

    @Test
    void sameInputsProduceSameOutput() {
      LocalDate date = LocalDate.of(2026, 9, 10);
      String first = NamingPolicy.writeIndex(PrecalculateFamily.VIEW, 3, date);
      String second = NamingPolicy.writeIndex(PrecalculateFamily.VIEW, 3, date);
      String third = NamingPolicy.writeIndex(PrecalculateFamily.VIEW, 3, date);
      assertThat(second).isEqualTo(first);
      assertThat(third).isEqualTo(second);
    }

    @Test
    void indexSetIsIdempotentForAlreadyUnderscored() {
      String once = NamingPolicy.indexSet("rum_global_view_precalculate_auto_1");
      String twice = NamingPolicy.indexSet(once);
      assertThat(twice).isEqualTo(once);
    }

    @Test
    void searchPatternIsIndexSetPlusStar() {
      for (PrecalculateFamily family : PrecalculateFamily.values()) {
        for (int n = 1; n <= PrecalculateStorage.DEFAULT_DISPERSED_COUNT; n++) {
          String indexSet = NamingPolicy.indexSet(family, n);
          assertThat(NamingPolicy.searchPattern(family, n)).isEqualTo(indexSet + "*");
        }
      }
    }
  }
}
