// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import com.tencent.bk.bkmonitor.rum.model.Span;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import javax.annotation.Nullable;
import org.apache.flink.util.StringUtils;

/** Session/View 的租户身份编码，供 keyed state 和 ES 文档 ID 共用。 */
public final class RumEntityKey {

    private RumEntityKey() {}

    /** entityId 由上游过滤保证非空；缺少 Span 时仍返回 key，让算子执行身份缺失审计。 */
    public static String of(@Nullable Span span, String entityId) {
        return of(span == null ? null : span.getBkBizId(),
                span == null ? null : span.getAppName(), entityId);
    }

    /**
     * 长度前缀允许应用名和原始 ID 包含分隔符，不修剪或截断身份字段。
     *
     * <p>缺失业务/应用分别编码为 null/-1；算子会在访问聚合状态前拒绝这类事件。
     */
    public static String of(@Nullable Integer bkBizId, @Nullable String appName, String entityId) {
        String appPart = appName == null ? "-1:" : appName.length() + ":" + appName;
        return bkBizId + ":" + appPart + entityId.length() + ":" + entityId;
    }

    /** 将租户与实体身份摘要拼接窗口 ID，隔离重开窗口，并限制长应用名对 ES 主键长度的影响。 */
    public static String documentId(
            Integer bkBizId, String appName, String entityId, String windowId) {
        byte[] key = of(bkBizId, appName, entityId).getBytes(StandardCharsets.UTF_8);
        try {
            return StringUtils.byteToHexString(MessageDigest.getInstance("SHA-256").digest(key))
                    + ":" + windowId;
        } catch (NoSuchAlgorithmException e) {
            // SHA-256 是 Java 运行时必须提供的算法。
            throw new IllegalStateException("SHA-256 is unavailable", e);
        }
    }
}
