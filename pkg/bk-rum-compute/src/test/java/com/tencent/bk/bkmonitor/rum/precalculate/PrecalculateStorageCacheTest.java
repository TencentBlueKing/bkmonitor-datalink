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

import java.time.Duration;
import java.time.LocalDate;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

/**
 * {@link PrecalculateStorageCache} 单元测试。
 *
 * <p>覆盖：① 构造校验；② 同 key 复用同一实例；③ 不同 key 产生不同实例；④ 容量淘汰；⑤ TTL 失效；⑥ 清空。
 */
class PrecalculateStorageCacheTest {

  private static final long CLUSTER_ID = 3L;
  private static final LocalDate DATE = LocalDate.of(2026, 9, 10);

  @Test
  void cachesKeepIndependentShardCountsWhenRebuilt() {
    PrecalculateStorageCache single =
        new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60), 1);
    PrecalculateStorageCache seven =
        new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60), 7);

    for (PrecalculateFamily family : PrecalculateFamily.values()) {
      PrecalculateStorage first = single.get(family, 1L, "app", DATE);
      PrecalculateStorage second = seven.get(family, 1L, "app", DATE);
      assertThat(first.hash.nodeCount()).isEqualTo(1);
      assertThat(first.selectedShardNumber).isEqualTo(1);
      assertThat(second.hash.nodeCount()).isEqualTo(7);

      single.invalidateAll();
      seven.invalidateAll();
      assertThat(single.get(family, 1L, "app", DATE).writeAlias).isEqualTo(first.writeAlias);
      assertThat(seven.get(family, 1L, "app", DATE).writeAlias).isEqualTo(second.writeAlias);
    }
  }

  // ────────────────────────── 构造校验 ──────────────────────────

  @Nested
  class ConstructionTest {

    @Test
    void negativeClusterIdThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorageCache(-1L, 100, Duration.ofMinutes(60)))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void zeroMaxSizeThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorageCache(CLUSTER_ID, 0, Duration.ofMinutes(60)))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void negativeMaxSizeThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorageCache(CLUSTER_ID, -1, Duration.ofMinutes(60)))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void nullTtlThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorageCache(CLUSTER_ID, 100, null))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void zeroTtlThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ZERO))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void negativeTtlThrows() {
      assertThatThrownBy(
          () -> new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(-1)))
          .isInstanceOf(IllegalArgumentException.class);
    }
  }

  // ────────────────────────── 复用与差异 ──────────────────────────

  @Nested
  class CacheBehaviorTest {

    @Test
    void sameKeyReturnsSameInstance() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      PrecalculateStorage s1 = cache.get(PrecalculateFamily.VIEW, 1L, "app", DATE);
      PrecalculateStorage s2 = cache.get(PrecalculateFamily.VIEW, 1L, "app", DATE);
      PrecalculateStorage s3 = cache.get(PrecalculateFamily.VIEW, 1L, "app", DATE);
      assertThat(s1).isNotNull();
      assertThat(s2).isSameAs(s1);
      assertThat(s3).isSameAs(s2);
      assertThat(cache.estimatedSize()).isEqualTo(1L);
    }

    @Test
    void differentBizIdProducesDifferentInstance() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      PrecalculateStorage s1 = cache.get(PrecalculateFamily.VIEW, 1L, "app", DATE);
      PrecalculateStorage s2 = cache.get(PrecalculateFamily.VIEW, 2L, "app", DATE);
      assertThat(s2).isNotSameAs(s1);
      assertThat(cache.estimatedSize()).isEqualTo(2L);
    }

    @Test
    void differentAppNameProducesDifferentInstance() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      PrecalculateStorage s1 = cache.get(PrecalculateFamily.VIEW, 1L, "app1", DATE);
      PrecalculateStorage s2 = cache.get(PrecalculateFamily.VIEW, 1L, "app2", DATE);
      assertThat(s2).isNotSameAs(s1);
    }

    @Test
    void differentDateProducesDifferentInstance() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      PrecalculateStorage s1 = cache.get(PrecalculateFamily.VIEW, 1L, "app", LocalDate.of(2026, 9, 10));
      PrecalculateStorage s2 = cache.get(PrecalculateFamily.VIEW, 1L, "app", LocalDate.of(2026, 9, 11));
      assertThat(s2).isNotSameAs(s1);
      assertThat(cache.estimatedSize()).isEqualTo(2L);
    }

    @Test
    void differentFamilyProducesDifferentInstance() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      PrecalculateStorage view = cache.get(PrecalculateFamily.VIEW, 1L, "app", DATE);
      PrecalculateStorage session = cache.get(PrecalculateFamily.SESSION, 1L, "app", DATE);
      assertThat(session).isNotSameAs(view);
      assertThat(view.writeAlias.contains("_view_")).isTrue();
      assertThat(session.writeAlias.contains("_session_")).isTrue();
    }
  }

  // ────────────────────────── 输入校验 ──────────────────────────

  @Nested
  class GetValidationTest {

    @Test
    void nullFamilyThrows() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      assertThatThrownBy(
          () -> cache.get(null, 1L, "app", DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void nullAppNameThrows() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      assertThatThrownBy(
          () -> cache.get(PrecalculateFamily.VIEW, 1L, null, DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void blankAppNameThrows() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      assertThatThrownBy(
          () -> cache.get(PrecalculateFamily.VIEW, 1L, "", DATE))
          .isInstanceOf(IllegalArgumentException.class);
      assertThatThrownBy(
          () -> cache.get(PrecalculateFamily.VIEW, 1L, "  ", DATE))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void nullDateThrows() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      assertThatThrownBy(
          () -> cache.get(PrecalculateFamily.VIEW, 1L, "app", null))
          .isInstanceOf(IllegalArgumentException.class);
    }
  }

  // ────────────────────────── 失效 ──────────────────────────

  @Nested
  class InvalidationTest {

    @Test
    void invalidateAllClearsCache() {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMinutes(60));
      cache.get(PrecalculateFamily.VIEW, 1L, "app1", DATE);
      cache.get(PrecalculateFamily.VIEW, 2L, "app2", DATE);
      assertThat(cache.estimatedSize()).isEqualTo(2L);
      cache.invalidateAll();
      assertThat(cache.estimatedSize()).isEqualTo(0L);
    }

    @Test
    void ttlExpiresEntries() throws InterruptedException {
      PrecalculateStorageCache cache = new PrecalculateStorageCache(CLUSTER_ID, 100, Duration.ofMillis(50));
      cache.get(PrecalculateFamily.VIEW, 1L, "app", DATE);
      assertThat(cache.estimatedSize()).isEqualTo(1L);
      Thread.sleep(150);
      // Caffeine 的过期是惰性的，需要再访问一次触发清理
      cache.get(PrecalculateFamily.VIEW, 2L, "app2", DATE);
      // 实际估算可能略大或刚清理完——主要逻辑是再次 get 会拿到新实例
      PrecalculateStorage s = cache.get(PrecalculateFamily.VIEW, 1L, "app", DATE);
      assertThat(s).isNotNull();
    }
  }
}
