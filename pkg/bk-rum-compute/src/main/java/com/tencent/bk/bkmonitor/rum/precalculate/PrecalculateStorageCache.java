// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.precalculate;

import com.github.benmanes.caffeine.cache.Cache;
import com.github.benmanes.caffeine.cache.Caffeine;
import java.time.Duration;
import java.time.LocalDate;
import lombok.EqualsAndHashCode;
import lombok.Value;

/**
 * {@link PrecalculateStorage} 进程内缓存，避免每个事件都重建分表节点。
 *
 * <p>缓存 key = {@code (family, bkBizId, appName, clusterId, date, dispersedCount)}，
 * clusterId 和 dispersedCount 在构造时固定，date 由调用方按窗口首次事件时间确定。
 *
 * <h2>失效策略</h2>
 * <ul>
 *   <li>{@code maximumSize}：超过容量按 LRU 淘汰
 *   <li>{@code expireAfterWrite(ttl)}：写后超过 ttl 强制失效，跨天场景自动切换新 key
 * </ul>
 *
 * <h2>线程安全</h2>
 * Caffeine 内部已线程安全，多个 Flink sink writer 可并发调用 {@link #get}。
 */
public final class PrecalculateStorageCache {

  private final Cache<Key, PrecalculateStorage> cache;
  private final long clusterId;
  private final int dispersedCount;

  /**
   * @param clusterId ES 集群 ID（节点 key 的一部分），不可变
   * @param maxSize 最大缓存条目数（超过按 LRU 淘汰）
   * @param ttl 写入后多久失效（推荐 ≥ 5 分钟，覆盖跨分钟边界的事件）
   */
  public PrecalculateStorageCache(long clusterId, int maxSize, Duration ttl) {
    this(clusterId, maxSize, ttl, PrecalculateStorage.DEFAULT_DISPERSED_COUNT);
  }

  /** 缓存重建的路由始终使用该实例的分表数；分表数必须大于零。 */
  public PrecalculateStorageCache(long clusterId, int maxSize, Duration ttl, int dispersedCount) {
    if (clusterId < 0) {
      throw new IllegalArgumentException("clusterId must be non-negative, got " + clusterId);
    }
    if (maxSize <= 0) {
      throw new IllegalArgumentException("maxSize must be positive, got " + maxSize);
    }
    if (ttl == null || ttl.isZero() || ttl.isNegative()) {
      throw new IllegalArgumentException("ttl must be positive, got " + ttl);
    }
    if (dispersedCount < 1) {
      throw new IllegalArgumentException("dispersedCount must be >= 1, got: " + dispersedCount);
    }
    this.clusterId = clusterId;
    this.dispersedCount = dispersedCount;
    this.cache =
        Caffeine.newBuilder().maximumSize(maxSize).expireAfterWrite(ttl).build();
  }

  /**
   * 获取或构建指定 (family, bkBizId, appName, date) 的 {@link PrecalculateStorage}。
   *
   * @param family 预计算家族，不能为 null
   * @param bkBizId 业务 ID
   * @param appName 应用名，不能为 null/blank
   * @param date UTC 日期，不能为 null
   * @return 已缓存或新建的 PrecalculateStorage 实例
   */
  public PrecalculateStorage get(
      PrecalculateFamily family, long bkBizId, String appName, LocalDate date) {
    if (family == null) {
      throw new IllegalArgumentException("family must not be null");
    }
    if (appName == null || appName.isBlank()) {
      throw new IllegalArgumentException("appName must not be blank");
    }
    if (date == null) {
      throw new IllegalArgumentException("date must not be null");
    }
    Key key = new Key(family, bkBizId, appName, clusterId, date, dispersedCount);
    return cache.get(key, PrecalculateStorageCache::createStorage);
  }

  private static PrecalculateStorage createStorage(Key key) {
    return PrecalculateStorage.forApp(
        key.family, key.bkBizId, key.appName, key.clusterId, key.date, key.dispersedCount);
  }

  /** 清空所有缓存条目（一般用于运维或测试）。 */
  public void invalidateAll() {
    cache.invalidateAll();
  }

  /** 当前缓存条目数（估算值，非严格精确）。 */
  public long estimatedSize() {
    return cache.estimatedSize();
  }

  @Value
  @EqualsAndHashCode
  private static class Key {
    PrecalculateFamily family;
    long bkBizId;
    String appName;
    long clusterId;
    LocalDate date;
    int dispersedCount;
  }
}
