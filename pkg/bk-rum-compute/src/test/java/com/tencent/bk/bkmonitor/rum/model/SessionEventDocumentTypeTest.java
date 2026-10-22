// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.model;

import static org.assertj.core.api.Assertions.assertThat;

import org.apache.flink.api.common.ExecutionConfig;
import org.apache.flink.api.common.typeinfo.TypeInformation;
import org.apache.flink.api.common.typeutils.TypeSerializer;
import org.apache.flink.api.java.typeutils.PojoTypeInfo;
import org.apache.flink.api.java.typeutils.runtime.PojoSerializer;
import org.apache.flink.core.memory.DataInputDeserializer;
import org.apache.flink.core.memory.DataOutputSerializer;
import org.junit.jupiter.api.Test;

/** Locks the Session state serializer contract after removing its embedded bitmap. */
class SessionEventDocumentTypeTest {

  @Test
  void usesPojoSerializerForKeyedState() {
    TypeInformation<SessionEventDocument> typeInformation =
        TypeInformation.of(SessionEventDocument.class);

    TypeSerializer<SessionEventDocument> serializer =
        typeInformation.createSerializer(new ExecutionConfig().getSerializerConfig());

    assertThat(typeInformation).isInstanceOf(PojoTypeInfo.class);
    assertThat(serializer).isInstanceOf(PojoSerializer.class);
  }

  @Test
  void serializerPreservesWindowState() throws Exception {
    TypeSerializer<SessionEventDocument> serializer =
        TypeInformation.of(SessionEventDocument.class)
            .createSerializer(new ExecutionConfig().getSerializerConfig());
    SessionEventDocument document = new SessionEventDocument(64);
    document.setSessionId("session-1");
    document.setIndexDate("2026-09-10");
    document.setViewCount(2);
    document.setClosed(true);
    DataOutputSerializer output = new DataOutputSerializer(256);

    serializer.serialize(document, output);
    SessionEventDocument restored =
        serializer.deserialize(new DataInputDeserializer(output.getCopyOfBuffer()));

    assertThat(restored.getSessionId()).isEqualTo("session-1");
    assertThat(restored.getIndexDate()).isEqualTo("2026-09-10");
    assertThat(restored.getMaxStringChars()).isEqualTo(64);
    assertThat(restored.getViewCount()).isEqualTo(2);
    assertThat(restored.isClosed()).isTrue();
  }
}
