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
import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

/**
 * {@link PrecalculateStorage} 单元测试。
 *
 * <p>覆盖：① 构造校验；② 工厂方法（forApp / forId）；③ view/session 两族派生名一致性；④ 路由确定性
 * 与分布；⑤ 日期格式（含闰年）。
 */
class PrecalculateStorageTest {

  private static final long CLUSTER_ID = 3L;
  private static final LocalDate DATE = LocalDate.of(2026, 9, 10);

  // ────────────────────────── 构造校验 ──────────────────────────

  @Nested
  class ConstructionTest {

    @Test
    void nullFamilyThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorage(null, "123:app", CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void nullRouteKeyThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorage(PrecalculateFamily.VIEW, null, CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void blankRouteKeyThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorage(PrecalculateFamily.VIEW, "  ", CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void emptyRouteKeyThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorage(PrecalculateFamily.VIEW, "", CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void nullDateThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorage(PrecalculateFamily.VIEW, "123:app", CLUSTER_ID, null))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void allFieldsArePopulated() {
      PrecalculateStorage s =
          new PrecalculateStorage(PrecalculateFamily.VIEW, "123:app", CLUSTER_ID, DATE);
      assertThat(s.family).isEqualTo(PrecalculateFamily.VIEW);
      assertThat(s.routeKey).isEqualTo("123:app");
      assertThat(s.clusterId).isEqualTo(CLUSTER_ID);
      assertThat(s.date).isEqualTo(DATE);
      assertThat(s.hash).isNotNull();
      assertThat(s.selectedNodeKey).isNotNull();
      assertThat(s.selectedShardNumber >= 1 && s.selectedShardNumber <= 5).isTrue();
      assertThat(s.logicalTable).isNotNull();
      assertThat(s.indexSet).isNotNull();
      assertThat(s.searchPattern).isNotNull();
      assertThat(s.writeAlias).isNotNull();
    }

    @Test
    void hashHasFamilyDispersedCountNodes() {
      PrecalculateStorage s =
          new PrecalculateStorage(PrecalculateFamily.VIEW, "123:app", CLUSTER_ID, DATE);
      assertThat(s.hash.nodeCount()).isEqualTo(PrecalculateStorage.DEFAULT_DISPERSED_COUNT);

      PrecalculateStorage s2 =
          new PrecalculateStorage(PrecalculateFamily.SESSION, "123:app", CLUSTER_ID, DATE);
      assertThat(s2.hash.nodeCount()).isEqualTo(PrecalculateStorage.DEFAULT_DISPERSED_COUNT);
    }
  }

  // ────────────────────────── forApp 工厂 ────────────────────────

  @Nested
  class ForAppFactoryTest {

    @Test
    void routeKeyFormatIsBizIdColonAppName() {
      PrecalculateStorage s = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, DATE);
      assertThat(s.routeKey).isEqualTo("123:myApp");
    }

    @Test
    void acceptsZeroBizId() {
      PrecalculateStorage s = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 0L, "app", CLUSTER_ID, DATE);
      assertThat(s.routeKey).isEqualTo("0:app");
    }

    @Test
    void nullAppNameThrows() {
      assertThatThrownBy(
          () -> PrecalculateStorage.forApp(
              PrecalculateFamily.VIEW, 1L, null, CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void emptyAppNameThrows() {
      assertThatThrownBy(
          () -> PrecalculateStorage.forApp(
              PrecalculateFamily.VIEW, 1L, "", CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void blankAppNameThrows() {
      assertThatThrownBy(
          () -> PrecalculateStorage.forApp(
              PrecalculateFamily.VIEW, 1L, "  ", CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void derivedNamesMatchNamingPolicy() {
      // 验证内部一致性：所有派生名都通过 NamingPolicy 计算，selectedShard 是合法分表号
      PrecalculateStorage s = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, DATE);
      assertThat(
          s.logicalTable)
          .isEqualTo(NamingPolicy.logicalTable(PrecalculateFamily.VIEW, s.selectedShardNumber));
      assertThat(
          s.indexSet)
          .isEqualTo(NamingPolicy.indexSet(PrecalculateFamily.VIEW, s.selectedShardNumber));
      assertThat(
          s.searchPattern)
          .isEqualTo(NamingPolicy.searchPattern(PrecalculateFamily.VIEW, s.selectedShardNumber));
      assertThat(
          s.writeAlias)
          .isEqualTo(NamingPolicy.writeIndex(PrecalculateFamily.VIEW, s.selectedShardNumber, DATE));
    }
  }

  // ────────────────────────── forId 工厂 ─────────────────────────

  @Nested
  class ForIdFactoryTest {

    @Test
    void routeKeyIsId() {
      PrecalculateStorage s = PrecalculateStorage.forId(
          PrecalculateFamily.VIEW, "view_abc123", CLUSTER_ID, DATE);
      assertThat(s.routeKey).isEqualTo("view_abc123");
    }

    @Test
    void nullIdThrows() {
      assertThatThrownBy(
          () -> PrecalculateStorage.forId(
              PrecalculateFamily.VIEW, null, CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void blankIdThrows() {
      assertThatThrownBy(
          () -> PrecalculateStorage.forId(
              PrecalculateFamily.VIEW, "", CLUSTER_ID, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void derivedNamesMatchNamingPolicy() {
      PrecalculateStorage s = PrecalculateStorage.forId(
          PrecalculateFamily.SESSION, "session_abc123", CLUSTER_ID, DATE);
      assertThat(
          s.logicalTable)
          .isEqualTo(NamingPolicy.logicalTable(PrecalculateFamily.SESSION, s.selectedShardNumber));
      assertThat(
          s.writeAlias)
          .isEqualTo(NamingPolicy.writeIndex(PrecalculateFamily.SESSION, s.selectedShardNumber, DATE));
    }
  }

  // ────────────────────────── VIEW 家族 ──────────────────────────

  @Nested
  class ViewFamilyTest {

    @Test
    void logicalTableContainsViewPurpose() {
      PrecalculateStorage s = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 1L, "app", CLUSTER_ID, DATE);
      assertThat(s.logicalTable.startsWith("rum_global_view.precalculate_auto_")).isTrue();
      assertThat(s.indexSet.startsWith("rum_global_view_precalculate_auto_")).isTrue();
      assertThat(s.searchPattern.startsWith("rum_global_view_precalculate_auto_")).isTrue();
      assertThat(s.searchPattern.endsWith("*")).isTrue();
      assertThat(
          s.writeAlias.startsWith("write_20260910_rum_global_view_precalculate_auto_"))
          .isTrue();
    }

    @Test
    void selectedNodeKeyFormat() {
      PrecalculateStorage s = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 1L, "app", CLUSTER_ID, DATE);
      assertThat(
          s.selectedNodeKey.startsWith(CLUSTER_ID + "-rum_global_view.precalculate_auto_"))
          .isTrue();
    }
  }

  // ────────────────────────── SESSION 家族 ───────────────────────

  @Nested
  class SessionFamilyTest {

    @Test
    void logicalTableContainsSessionPurpose() {
      PrecalculateStorage s = PrecalculateStorage.forApp(
          PrecalculateFamily.SESSION, 1L, "app", CLUSTER_ID, DATE);
      assertThat(s.logicalTable.startsWith("rum_global_session.precalculate_auto_")).isTrue();
      assertThat(s.indexSet.startsWith("rum_global_session_precalculate_auto_")).isTrue();
      assertThat(s.searchPattern.startsWith("rum_global_session_precalculate_auto_")).isTrue();
      assertThat(s.searchPattern.endsWith("*")).isTrue();
      assertThat(
          s.writeAlias.startsWith("write_20260910_rum_global_session_precalculate_auto_"))
          .isTrue();
    }

    @Test
    void viewAndSessionRouteIndependently() {
      // 同一个 (bkBizId, appName) 在 VIEW 和 SESSION 上应该独立选分表
      PrecalculateStorage view = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 1L, "app", CLUSTER_ID, DATE);
      PrecalculateStorage session = PrecalculateStorage.forApp(
          PrecalculateFamily.SESSION, 1L, "app", CLUSTER_ID, DATE);
      // 各自走不同业务域前缀（拆分后）
      assertThat(view.logicalTable.startsWith("rum_global_view.precalculate_auto_")).isTrue();
      assertThat(session.logicalTable.startsWith("rum_global_session.precalculate_auto_")).isTrue();
      // 写别名也分别带 view / session 业务域
      assertThat(view.writeAlias.contains("rum_global_view_precalculate_auto_")).isTrue();
      assertThat(session.writeAlias.contains("rum_global_session_precalculate_auto_")).isTrue();
    }
  }

  // ────────────────────────── 确定性 ──────────────────────────────

  @Nested
  class DeterminismTest {

    @Test
    void sameInputsProduceSameShard() {
      PrecalculateStorage s1 = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, DATE);
      PrecalculateStorage s2 = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, DATE);
      PrecalculateStorage s3 = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, DATE);
      assertThat(s2.selectedShardNumber).isEqualTo(s1.selectedShardNumber);
      assertThat(s3.selectedShardNumber).isEqualTo(s2.selectedShardNumber);
      assertThat(s2.writeAlias).isEqualTo(s1.writeAlias);
      assertThat(s3.writeAlias).isEqualTo(s2.writeAlias);
    }

    @Test
    void differentDatesProduceDifferentWriteAlias() {
      PrecalculateStorage s1 = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, LocalDate.of(2026, 9, 10));
      PrecalculateStorage s2 = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, LocalDate.of(2026, 9, 11));
      // 同一分表，物理写索引因日期不同而不同
      assertThat(s2.selectedShardNumber).isEqualTo(s1.selectedShardNumber);
      assertThat(s1.writeAlias.contains("write_20260910_")).isTrue();
      assertThat(s2.writeAlias.contains("write_20260911_")).isTrue();
      assertThat(!s1.writeAlias.equals(s2.writeAlias)).isTrue();
    }

    @Test
    void leapYearDateWorks() {
      PrecalculateStorage s = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, LocalDate.of(2024, 2, 29));
      assertThat(s.writeAlias.startsWith("write_20240229_")).isTrue();
    }

    @Test
    void yearBoundaryDateWorks() {
      PrecalculateStorage s1 = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, LocalDate.of(2026, 12, 31));
      PrecalculateStorage s2 = PrecalculateStorage.forApp(
          PrecalculateFamily.VIEW, 123L, "myApp", CLUSTER_ID, LocalDate.of(2027, 1, 1));
      assertThat(s1.writeAlias.startsWith("write_20261231_")).isTrue();
      assertThat(s2.writeAlias.startsWith("write_20270101_")).isTrue();
    }
  }

  // ────────────────────────── 分布均匀性 ─────────────────────────

  @Nested
  class DistributionTest {

    @Test
    void differentAppNamesDistributeAcrossAllFiveShards() {
      // 1000 个不同的 (bkBizId, appName) 应充分分散到 5 个分表
      Set<Integer> hitShards = new HashSet<>();
      Map<Integer, Integer> shardCounts = new HashMap<>();
      for (int i = 0; i < 1000; i++) {
        PrecalculateStorage s = PrecalculateStorage.forApp(
            PrecalculateFamily.VIEW, i, "app_" + i, CLUSTER_ID, DATE);
        hitShards.add(s.selectedShardNumber);
        shardCounts.merge(s.selectedShardNumber, 1, Integer::sum);
      }
      // 5 个分表都应被命中（极小概率漏）
      assertThat(
          hitShards.size() >= 4)
          .withFailMessage("expected ≥4 shards hit, got " + hitShards.size())
          .isTrue();
      // 每个分表至少分到一些
      for (int n = 1; n <= 5; n++) {
        assertThat(
            shardCounts.getOrDefault(n, 0) > 0)
            .withFailMessage("shard " + n + " got zero keys")
            .isTrue();
      }
    }
  }
}
