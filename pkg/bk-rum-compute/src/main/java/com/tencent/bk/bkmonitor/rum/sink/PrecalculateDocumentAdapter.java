// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink;

import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateFamily;
import java.io.Serializable;
import java.util.Map;

/**
 * 预计算记录的业务适配契约。
 *
 * <p>适配器只负责解释某一类聚合文档的路由字段和 ES 字段，不负责缓存、请求构造或连接管理。接口继承
 * {@link Serializable}，因为适配器会随 Flink sink emitter 一起序列化。
 */
public interface PrecalculateDocumentAdapter<T> extends Serializable {

  /** 返回文档所属的预计算家族。 */
  PrecalculateFamily family();

  /** 判断记录是否具备写入所需的业务 ID、应用名和 document ID。 */
  boolean hasRequiredRoutingFields(T record);

  /** 返回用于预计算存储路由的业务 ID。 */
  long bkBizId(T record);

  /** 返回用于预计算存储路由的应用名。 */
  String appName(T record);

  /** 返回 ES 覆盖写使用的 document ID。 */
  String documentId(T record);

  /** 返回用于选择预计算物理索引的稳定 UTC 日期（yyyy-MM-dd）。 */
  String indexDate(T record);

  /** 返回不包含通用 {@code time} 字段的 ES 字段映射。 */
  Map<String, Object> toDocumentFields(T record);
}
