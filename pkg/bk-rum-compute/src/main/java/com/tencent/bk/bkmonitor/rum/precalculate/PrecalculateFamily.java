// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.precalculate;

/**
 * 预计算表家族枚举：所有命名规则都由此枚举驱动，{@link NamingPolicy} 仅做字符串拼接。
 *
 * <p>每个家族定义一个 {@code (businessPrefix, purpose)} 二元组，分表数量由运行时参数
 * {@code precalculate.dispersed-count} 决定（默认 5）。
 *
 * <pre>{@code
 * {businessPrefix}precalculate_auto_{N}                            // 逻辑表（SQL）
 * {businessPrefix_underscored}precalculate_auto_{N}                 // ES index_set
 * write_{yyyyMMdd}_{businessPrefix_underscored}precalculate_auto_{N}  // 物理写索引
 * {businessPrefix_underscored}precalculate_auto_{N}*                // 物理读索引通配
 * }</pre>
 *
 * <p>当前支持的家族（view / session 各占独立业务域）：
 *
 * <ul>
 *   <li>{@link #VIEW} — RUM view 聚合，业务域 {@code rum_global_view}
 *   <li>{@link #SESSION} — RUM session 聚合，业务域 {@code rum_global_session}
 * </ul>
 *
 * @see NamingPolicy
 * @see <a href="../../../../../../docs/index-naming.md">docs/index-naming.md</a>
 */
public enum PrecalculateFamily {

  /** RUM view 预计算，业务域 {@code rum_global_view}。 */
  VIEW("rum_global_view.", "view"),

  /** RUM session 预计算，业务域 {@code rum_global_session}。 */
  SESSION("rum_global_session.", "session");

  private final String businessPrefix;
  private final String purpose;

  PrecalculateFamily(String businessPrefix, String purpose) {
    this.businessPrefix = businessPrefix;
    this.purpose = purpose;
  }

  /** 业务域前缀（含 {@code "."}）：{@code "rum_global_view."} 或 {@code "rum_global_session."}。 */
  public String businessPrefix() {
    return businessPrefix;
  }

  /** 预计算用途：{@code "view"} / {@code "session"}。 */
  public String purpose() {
    return purpose;
  }

  /**
   * 逻辑表前缀（含 {@code "."}）：{@code "rum_global_view.precalculate_auto_"}。
   *
   * <p>拼接分表号得到完整逻辑表名。
   */
  public String logicalTablePrefix() {
    return businessPrefix + "precalculate_auto_";
  }

  /**
   * ES index_set 前缀（{@code "."} → {@code "_"}）：{@code "rum_global_view_precalculate_auto_"}。
   *
   * <p>拼接分表号得到完整 index_set 名称，也是读别名。
   */
  public String indexSetPrefix() {
    return businessPrefix.replace('.', '_') + "precalculate_auto_";
  }
}
