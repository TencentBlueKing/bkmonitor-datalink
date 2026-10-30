// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.precalculate;

import java.time.LocalDate;
import java.util.ArrayList;
import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * 预计算存储路由：给定 (family, routeKey, clusterId, date) 解析出最终写入的物理索引名。
 *
 * <p>对应 BK-Monitor Python 端 {@code bkmonitor/apm/core/discover/precalculation/storage.py:PrecalculateStorage}。
 * 构造时一次性完成：① 用 {@link NamingPolicy} 构造 N 个分表的节点 key；② 用 {@link RendezvousHash} 选出本 (routeKey,
 * date) 应写入的分表；③ 推导出逻辑表 / index_set / 读通配 / 写别名。
 *
 * <p>分表数量 N 由运行时参数 {@code precalculate.dispersed-count} 决定（默认 5）。
 *
 * <p>典型用法：
 *
 * <pre>{@code
 * PrecalculateStorage s = PrecalculateStorage.forId(
 *     PrecalculateFamily.VIEW, viewId, clusterId, today);
 *
 * IndexRequest req = new IndexRequest(s.writeAlias())
 *     .id(viewId)
 *     .source(document);
 * client.index(req, RequestOptions.DEFAULT);
 * }</pre>
 *
 * <p>本类只持有路由结果，**不做实际的 ES 写入**——上层 sink / 定时任务据此拼 IndexRequest 再调 bulk API。
 *
 * <p>所有字段在构造后不可变，线程安全。
 *
 * @see NamingPolicy
 * @see RendezvousHash
 * @see <a href="../../../../../../docs/index-naming.md">docs/index-naming.md §2.3 命名生成器</a>
 */
public final class PrecalculateStorage {

  /** 未配置分表数时使用的节点数量。 */
  public static final int DEFAULT_DISPERSED_COUNT = 5;

  private static final Logger LOG = LoggerFactory.getLogger(PrecalculateStorage.class);

  /** 预计算家族（view / session）。 */
  public final PrecalculateFamily family;

  /** 路由 key：viewId / sessionId。 */
  public final String routeKey;

  /** ES 集群 ID（来自 metadata 服务）。 */
  public final long clusterId;

  /** 该物理索引归属的 UTC 日期。 */
  public final LocalDate date;

  /** 当前路由实例的分表数，不依赖进程级可变配置。 */
  public final int dispersedCount;

  /** 内部使用的 Rendezvous Hash。 */
  public final RendezvousHash hash;

  /** 本 routeKey 命中的分表号（[1, {@link #dispersedCount}]）。 */
  public final int selectedShardNumber;

  /** 命中的分表节点 key：{@code "{clusterId}-{tableName}"}。 */
  public final String selectedNodeKey;

  /** 命中的逻辑表名：{@code "rum_global.precalculate_{kind}_auto_N"}。 */
  public final String logicalTable;

  /** 命中的 ES index_set 名称：{@code "rum_global_precalculate_{kind}_auto_N"}（也是读别名）。 */
  public final String indexSet;

  /** 读索引通配：{@code "{indexSet}*"}。 */
  public final String searchPattern;

  /** 物理写索引别名：{@code "write_{yyyyMMdd}_{indexSet}"}。 */
  public final String writeAlias;

  /**
   * 低阶构造函数：直接传入已计算好的 routeKey。
   *
   * <p>多数场景请用 {@link #forApp} 或 {@link #forId}。
   *
   * @param family 预计算家族，不能为 null
   * @param routeKey 路由 key（viewId / sessionId），不能为空
   * @param clusterId ES 集群 ID
   * @param date UTC 日期
   */
  public PrecalculateStorage(
      PrecalculateFamily family, String routeKey, long clusterId, LocalDate date) {
    this(family, routeKey, clusterId, date, DEFAULT_DISPERSED_COUNT);
  }

  /** 按显式分表数构建路由；分表数必须大于零，并与目标索引配置一致。 */
  public PrecalculateStorage(
      PrecalculateFamily family, String routeKey, long clusterId, LocalDate date,
      int dispersedCount) {
    if (family == null) {
      throw new IllegalArgumentException("family must not be null");
    }
    if (routeKey == null || routeKey.isBlank()) {
      throw new IllegalArgumentException("routeKey must not be blank");
    }
    if (date == null) {
      throw new IllegalArgumentException("date must not be null");
    }
    if (dispersedCount < 1) {
      throw new IllegalArgumentException("dispersedCount must be >= 1, got: " + dispersedCount);
    }

    this.family = family;
    this.routeKey = routeKey;
    this.clusterId = clusterId;
    this.date = date;
    this.dispersedCount = dispersedCount;

    // 构造 N 个分表节点
    List<String> nodes = new ArrayList<>(dispersedCount);
    for (int n = 1; n <= dispersedCount; n++) {
      nodes.add(NamingPolicy.nodeKey(clusterId, NamingPolicy.logicalTable(family, n)));
    }
    this.hash = new RendezvousHash(nodes);

    // 选分表
    this.selectedNodeKey = hash.selectNode(routeKey);
    this.selectedShardNumber = parseShardNumber(selectedNodeKey, family, dispersedCount);

    // 派生所有索引名
    this.logicalTable = NamingPolicy.logicalTable(family, selectedShardNumber);
    this.indexSet = NamingPolicy.indexSet(family, selectedShardNumber);
    this.searchPattern = NamingPolicy.searchPattern(family, selectedShardNumber);
    this.writeAlias = NamingPolicy.writeIndex(family, selectedShardNumber, date);

    LOG.debug(
        "PrecalculateStorage resolved: family={}, routeKey={}, clusterId={}, date={}, "
            + "shard={}, writeAlias={}",
        family, routeKey, clusterId, date, selectedShardNumber, writeAlias);
  }

  /**
   * 工厂方法：独占 trace data_id 场景（按 {@code "{bkBizId}:{appName}"} 路由）。
   */
  public static PrecalculateStorage forApp(
      PrecalculateFamily family, long bkBizId, String appName, long clusterId, LocalDate date) {
    return forApp(family, bkBizId, appName, clusterId, date, DEFAULT_DISPERSED_COUNT);
  }

  /** 应用路由使用调用方的分表配置，避免多个作业共享 JVM 时互相影响。 */
  public static PrecalculateStorage forApp(
      PrecalculateFamily family, long bkBizId, String appName, long clusterId, LocalDate date,
      int dispersedCount) {
    if (appName == null || appName.isBlank()) {
      throw new IllegalArgumentException("appName must not be blank");
    }
    return new PrecalculateStorage(
        family, NamingPolicy.routeKey(bkBizId, appName), clusterId, date, dispersedCount);
  }

  /**
   * 工厂方法：直接使用 viewId / sessionId 作为路由 key。
   *
   * @param family 预计算家族
   * @param id viewId 或 sessionId（不能为空）
   * @param clusterId ES 集群 ID
   * @param date UTC 日期
   */
  public static PrecalculateStorage forId(
      PrecalculateFamily family, String id, long clusterId, LocalDate date) {
    if (id == null || id.isBlank()) {
      throw new IllegalArgumentException("id must not be blank");
    }
    return new PrecalculateStorage(family, id, clusterId, date);
  }

  /**
   * 从节点 key 解析分表号。节点 key 格式：{@code "{clusterId}-{logicalTable}"}，
   * 其中 {@code logicalTable} = {@code "{family.logicalTablePrefix()}{N}"}。
   *
   * <p>由于 clusterId 为数字、family.logicalTablePrefix 不含 {@code "-"}，可用首个 {@code "-"} 切分。
   */
  private static int parseShardNumber(
      String nodeKey, PrecalculateFamily family, int dispersedCount) {
    int dashIdx = nodeKey.indexOf('-');
    if (dashIdx < 0) {
      throw new IllegalStateException("malformed nodeKey (no '-'): " + nodeKey);
    }
    String logicalTable = nodeKey.substring(dashIdx + 1);
    String prefix = family.logicalTablePrefix();
    if (!logicalTable.startsWith(prefix)) {
      throw new IllegalStateException(
          "logicalTable '"
              + logicalTable
              + "' doesn't start with family prefix '"
              + prefix
              + "'");
    }
    String suffix = logicalTable.substring(prefix.length());
    int n;
    try {
      n = Integer.parseInt(suffix);
    } catch (NumberFormatException e) {
      throw new IllegalStateException(
          "malformed shard suffix in nodeKey '" + nodeKey + "': '" + suffix + "'", e);
    }
    if (n < 1 || n > dispersedCount) {
      throw new IllegalStateException(
          "shard number " + n + " out of range [1, " + dispersedCount + "]");
    }
    return n;
  }
}
