// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import static org.assertj.core.api.Assertions.assertThat;

import org.junit.jupiter.api.Test;

class RumEntityKeyTest {

    @Test
    void keepsFieldBoundariesWhenIdentifiersContainSeparators() {
        assertThat(RumEntityKey.of(1, "a:b", "c"))
                .isNotEqualTo(RumEntityKey.of(1, "a", "b:c"));
        assertThat(RumEntityKey.documentId(1, "a:b", "c", "window-1"))
                .isNotEqualTo(RumEntityKey.documentId(1, "a", "b:c", "window-1"));
    }

    @Test
    void identityPreservesWhitespaceAndUnicode() {
        assertThat(RumEntityKey.of(42, "应用😀", " 会话 ")).isEqualTo("42:4:应用😀4: 会话 ");
        assertThat(RumEntityKey.of(42, " app", "session"))
                .isNotEqualTo(RumEntityKey.of(42, "app", "session"));
    }

    @Test
    void documentIdMatchesStableSha256Encoding() {
        assertThat(RumEntityKey.of(42, "app", "session")).isEqualTo("42:3:app7:session");
        assertThat(RumEntityKey.documentId(42, "app", "session", "window-1"))
                .isEqualTo("80bbdf72f011b975c70f86d3c81298766f7999acd3a434f99b95348e491f0d4d:window-1");
    }

    @Test
    void missingDimensionsStillHaveNonNullKeysForOperatorAuditing() {
        assertThat(RumEntityKey.of(null, "session")).isNotNull();
        assertThat(RumEntityKey.of(null, "app", "session"))
                .isNotEqualTo(RumEntityKey.of(0, "app", "session"));
        assertThat(RumEntityKey.of(1, null, "session"))
                .isNotEqualTo(RumEntityKey.of(1, "", "session"));
    }
}
