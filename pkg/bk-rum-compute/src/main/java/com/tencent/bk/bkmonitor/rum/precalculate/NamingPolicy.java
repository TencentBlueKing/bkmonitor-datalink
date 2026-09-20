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
import java.time.format.DateTimeFormatter;

/**
 * 预计算索引命名规则的权威实现。所有索引名 / 路由 key / 节点 key 的字符串拼接都应通过本类完成，禁止散落到业务代码里手写。
 *
 * <h2>命名规则速查</h2>
 *
 * <pre>{@code
 * 逻辑表:   rum_global.precalculate_{purpose}_auto_{N}
 * 索引集:   rum_global_precalculate_{purpose}_auto_{N}
 * 物理写:   write_{yyyyMMdd}_rum_global_precalculate_{purpose}_auto_{N}
 * 物理读:   rum_global_precalculate_{purpose}_auto_{N}*
 * 节点 key: {clusterId}-{tableName}
 * 独占路由: {bkBizId}:{appName}
 * 共享路由: 0:{resultTableId}
 * }</pre>
 *
 * <p>任何修改都必须同步到 BK-Monitor Python 端的 {@code bkmonitor/apm/core/discover/precalculation/storage.py}，
 * 否则会导致同一 trace 路由到不同分表、查询时找不到数据。
 *
 * @see PrecalculateFamily
 * @see RumGlobalTablePrefix
 * @see <a href="../../../../../../docs/index-naming.md">docs/index-naming.md</a>
 */
public final class NamingPolicy {

  /** 物理写索引的日期段格式：无分隔、UTC。 */
  public static final String DATE_FORMAT = "yyyyMMdd";

  /** 物理写索引的前缀：{@code "write_"}。 */
  public static final String WRITE_PREFIX = "write_";

  /** 物理写索引中日期与 indexSet 之间的分隔符：{@code "_"}。 */
  public static final String WRITE_DATE_SEPARATOR = "_";

  /** 共享 trace data_id 路由 key 的业务维度占位符：{@code "0"}。 */
  public static final String SHARED_ROUTE_BIZ_PLACEHOLDER = "0";

  /** 节点 key 分隔符：{@code "-"}。 */
  public static final String NODE_KEY_SEPARATOR = "-";

  /** 路由 key 分隔符：{@code ":"}。 */
  public static final String ROUTE_KEY_SEPARATOR = ":";

  private static final DateTimeFormatter DATE_FORMATTER = DateTimeFormatter.ofPattern(DATE_FORMAT);

  private NamingPolicy() {}

  // ─────────────────────── 枚举驱动主 API ──────────────────────

  /**
   * 生成指定家族的逻辑表名（含业务域前缀与 {@code "."} 分隔）。
   *
   * <p>对应 BK-Monitor Python 端对应家族的 result_table_id。
   *
   * @param family 预计算家族，不能为 null
   * @param shardNumber 正整数分表号；可用节点范围由 {@link PrecalculateStorage} 的实例配置决定
   * @return 形如 {@code "rum_global.precalculate_view_auto_3"}
   * @throws IllegalArgumentException family 为 null 或 shardNumber 越界
   */
  public static String logicalTable(PrecalculateFamily family, int shardNumber) {
    validateFamily(family);
    validateShardNumber(shardNumber);
    return family.logicalTablePrefix() + shardNumber;
  }

  /**
   * 逻辑表 → ES index_set 名称：把 {@code "."} 替换为 {@code "_"}。
   *
   * <p>该名称对应 BK-Monitor metadata 中的 index_set 字段，也是 ES 端读别名（被一组 {@code write_*} 物理索引指向）。
   */
  public static String indexSet(PrecalculateFamily family, int shardNumber) {
    return indexSet(family, logicalTable(family, shardNumber));
  }

  /**
   * 通用版：任意家族的逻辑表名 → index_set。仅做字符替换，不校验业务域。
   *
   * @param family 预计算家族（决定 prefix 风格，不参与转换），不能为 null
   * @param logicalTable 逻辑表名（含 {@code "."}），不能为空
   */
  public static String indexSet(PrecalculateFamily family, String logicalTable) {
    validateFamily(family);
    validateNotBlank(logicalTable, "logicalTable");
    return logicalTable.replace('.', '_');
  }

  /**
   * 物理写索引别名（按天滚动）：{@code write_{yyyyMMdd}_{indexSet}}。
   *
   * <p>对应 BK-Monitor Python 端 {@code get_index_write_alias(index_name)} 的返回值。
   */
  public static String writeIndex(PrecalculateFamily family, int shardNumber, LocalDate date) {
    return writeIndex(indexSet(family, shardNumber), date);
  }

  /**
   * 物理读索引通配模式：{@code {indexSet}*}，可匹配该 index_set 下的所有 {@code write_*} 物理索引。
   *
   * <p>对应 BK-Monitor Python 端 {@code search_index_name = origin_index_name + "*"}。
   */
  public static String searchPattern(PrecalculateFamily family, int shardNumber) {
    return searchPattern(indexSet(family, shardNumber));
  }

  // ─────────────────────── 与家族无关的命名 ──────────────────────

  /**
   * 通用版：任意 index_set + 日期 → 物理写索引别名。无家族信息也能调用，常用于路由后落库。
   *
   * @param indexSet ES index_set 名称（不能含 {@code "."}）
   * @param date UTC 日期
   * @throws IllegalArgumentException indexSet 为空或含点，或 date 为 null
   */
  public static String writeIndex(String indexSet, LocalDate date) {
    validateIndexSet(indexSet);
    if (date == null) {
      throw new IllegalArgumentException("date must not be null");
    }
    return WRITE_PREFIX + date.format(DATE_FORMATTER) + WRITE_DATE_SEPARATOR + indexSet;
  }

  /**
   * 通用版：任意 index_set → 读索引通配。
   */
  public static String searchPattern(String indexSet) {
    validateIndexSet(indexSet);
    return indexSet + "*";
  }

  /**
   * 通用版：任意逻辑表名 → index_set。仅做字符替换，无家族参与。
   */
  public static String indexSet(String logicalTable) {
    validateNotBlank(logicalTable, "logicalTable");
    return logicalTable.replace('.', '_');
  }

  // ─────────────────────── 路由 / 节点 key ──────────────────────

  /**
   * 独占 trace data_id 场景的路由 key：{@code "{bkBizId}:{appName}"}。
   *
   * <p>对应 BK-Monitor Python 端 {@code get_route_key(bk_biz_id, app_name)} 的非共享分支。
   */
  public static String routeKey(long bkBizId, String appName) {
    validateNotBlank(appName, "appName");
    return bkBizId + ROUTE_KEY_SEPARATOR + appName;
  }

  /**
   * Rendezvous Hash 节点 key：{@code "{clusterId}-{tableName}"}。
   *
   * <p>对应 BK-Monitor Python 端 {@code StorageNode.key}。
   */
  public static String nodeKey(long clusterId, String tableName) {
    validateNotBlank(tableName, "tableName");
    return clusterId + NODE_KEY_SEPARATOR + tableName;
  }

  // ─────────────────────── 校验 ──────────────────────

  private static void validateFamily(PrecalculateFamily family) {
    if (family == null) {
      throw new IllegalArgumentException("family must not be null");
    }
  }

  private static void validateShardNumber(int shardNumber) {
    if (shardNumber < 1) {
      throw new IllegalArgumentException("shardNumber must be >= 1, got " + shardNumber);
    }
  }

  private static void validateNotBlank(String value, String name) {
    if (value == null || value.isBlank()) {
      throw new IllegalArgumentException(name + " must not be blank");
    }
  }

  private static void validateIndexSet(String indexSet) {
    validateNotBlank(indexSet, "indexSet");
    if (indexSet.indexOf('.') >= 0) {
      throw new IllegalArgumentException(
          "indexSet must not contain '.', got '" + indexSet + "'");
    }
  }
}
