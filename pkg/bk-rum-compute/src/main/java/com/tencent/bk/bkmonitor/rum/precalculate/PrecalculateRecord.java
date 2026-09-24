// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.precalculate;

import java.util.Map;

/**
 * 预计算 sink 的通用记录契约：暴露路由所需字段 + 文档序列化方法。
 *
 * <p>实现此接口的 POJO 可直接喂给 {@code PrecalculateElasticsearchSink.create(parameters, family)}。
 *
 * <p>已有 POJO（如 {@code ViewEventDocument} / {@code SessionEventDocument}）请使用对应的便捷工厂方法
 * {@code PrecalculateElasticsearchSink.forView} / {@code forSession}。
 *
 * @see PrecalculateElasticsearchSink
 */
public interface PrecalculateRecord {

  /** 蓝鲸业务 ID（路由 key 的一部分）。 */
  long bkBizId();

  /** RUM 应用名（路由 key 的一部分）。 */
  String appName();

  /** ES document id（覆盖写语义）。 */
  String documentId();

  /** 用于选择物理索引的稳定 UTC 日期（yyyy-MM-dd）。 */
  String indexDate();

  /** 把当前记录序列化为 ES 文档字段映射（与 ES 模板一致）。 */
  Map<String, Object> toDocument();
}
