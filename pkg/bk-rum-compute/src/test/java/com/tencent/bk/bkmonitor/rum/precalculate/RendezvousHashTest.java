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

import java.math.BigInteger;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

/**
 * {@link RendezvousHash} 单元测试。
 *
 * <p>覆盖：① 构造校验；② 路由确定性；③ 均匀分布；④ Rendezvous 稳定性（增删节点只重映射 K/N key）；⑤ 与 BK-Monitor Python
 * 端的 SHA-1 / BigInteger 一致性；⑥ 与 {@link NamingPolicy} 的端到端集成。
 */
class RendezvousHashTest {

  // ────────────────────────── 构造校验 ──────────────────────────

  @Nested
  class ConstructionTest {

    @Test
    void nullNodesThrows() {
      assertThatThrownBy(
          () -> new RendezvousHash(null))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void emptyNodesThrows() {
      assertThatThrownBy(
          () -> new RendezvousHash(Collections.emptyList()))
          .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void nodeCountReturnsSize() {
      RendezvousHash hash = new RendezvousHash(List.of("a", "b", "c"));
      assertThat(hash.nodeCount()).isEqualTo(3);
    }

    @Test
    void nodeListIsDefensivelyCopied() {
      List<String> mutable = new ArrayList<>(List.of("a", "b"));
      RendezvousHash hash = new RendezvousHash(mutable);
      mutable.add("c");
      assertThat(hash.nodeCount()).isEqualTo(2);
    }
  }

  // ────────────────────────── selectNode ────────────────────────

  @Nested
  class SelectNodeTest {

    @Test
    void nullRouteKeyThrows() {
      RendezvousHash hash = new RendezvousHash(List.of("a"));
      assertThatThrownBy(() -> hash.selectNode(null)).isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void singleNodeAlwaysReturnsThatNode() {
      RendezvousHash hash = new RendezvousHash(List.of("only"));
      for (int i = 0; i < 100; i++) {
        assertThat(hash.selectNode("key_" + i)).isEqualTo("only");
      }
    }

    @Test
    void sameInputProducesSameOutput() {
      RendezvousHash hash = new RendezvousHash(List.of("node_a", "node_b", "node_c"));
      String first = hash.selectNode("key1");
      String second = hash.selectNode("key1");
      String third = hash.selectNode("key1");
      assertThat(second).isEqualTo(first);
      assertThat(third).isEqualTo(second);
      assertThat(first).isNotNull();
    }

    @Test
    void selectedNodeIsOneOfTheRegisteredNodes() {
      List<String> nodes = List.of("n1", "n2", "n3", "n4", "n5");
      RendezvousHash hash = new RendezvousHash(nodes);
      for (int i = 0; i < 200; i++) {
        String selected = hash.selectNode("key_" + i);
        assertThat(
            nodes.contains(selected))
            .withFailMessage("selected=" + selected + " not in nodes=" + nodes)
            .isTrue();
      }
    }

    @Test
    void nodeOrderDoesNotAffectResult() {
      // 算法是顺序无关的：交换节点顺序，相同 route key 必须选到同一节点
      List<String> order1 = List.of("node_a", "node_b", "node_c");
      List<String> order2 = List.of("node_c", "node_a", "node_b");
      RendezvousHash h1 = new RendezvousHash(order1);
      RendezvousHash h2 = new RendezvousHash(order2);
      for (int i = 0; i < 50; i++) {
        String key = "key_" + i;
        assertThat(h2.selectNode(key)).isEqualTo(h1.selectNode(key));
      }
    }
  }

  // ────────────────────────── 分布均匀性 ────────────────────────

  @Nested
  class DistributionTest {

    @Test
    void fiveNodesDistributeRoughlyUniformly() {
      List<String> nodes =
          List.of("n1", "n2", "n3", "n4", "n5");
      RendezvousHash hash = new RendezvousHash(nodes);

      // 5000 个 key，期望每个节点分到约 1000 个；允许 ±20% 容差（统计噪声）
      int total = 5000;
      Map<String, Integer> counts = new HashMap<>();
      for (int i = 0; i < total; i++) {
        String selected = hash.selectNode("biz_" + i);
        counts.merge(selected, 1, Integer::sum);
      }
      for (String node : nodes) {
        int count = counts.getOrDefault(node, 0);
        // 期望 ~1000，宽松区间 [500, 1500]
        assertThat(
            count >= 500 && count <= 1500)
            .withFailMessage(node + " got " + count + " keys (expected ~1000, range [500,1500])")
            .isTrue();
      }
      // 每个节点至少分到一些（避免出现"死节点"）
      for (String node : nodes) {
        assertThat(
            counts.getOrDefault(node, 0) > 0)
            .withFailMessage(node + " received zero keys")
            .isTrue();
      }
    }
  }

  // ────────────────────────── Rendezvous 稳定性 ─────────────────
  //
  // Rendezvous Hash 的核心特性：增加一个节点时，只有 ~K/(N+1) 的 key 被重映射，
  // 不是所有 key。这是相对一致性哈希的优势。

  @Nested
  class RendezvousStabilityTest {

    @Test
    void addingNodeReassignsOnlyKOverNPlusOneKeys() {
      List<String> initial = List.of("n1", "n2", "n3", "n4", "n5");
      List<String> expanded = List.of("n1", "n2", "n3", "n4", "n5", "n6");
      RendezvousHash h1 = new RendezvousHash(initial);
      RendezvousHash h2 = new RendezvousHash(expanded);

      int total = 1000;
      int reassigned = 0;
      for (int i = 0; i < total; i++) {
        String key = "biz_" + i;
        if (!h1.selectNode(key).equals(h2.selectNode(key))) {
          reassigned++;
        }
      }
      // 期望 ~1000/6 ≈ 166 个 key 被重映射；允许 [100, 300] 容差
      assertThat(
          reassigned >= 100 && reassigned <= 300)
          .withFailMessage("reassigned=" + reassigned + " keys (expected ~" + total / 6 + ", range [100,300])")
          .isTrue();
    }
  }

  // ────────────────────────── 跨语言一致性 ─────────────────────
  //
  // 与 BK-Monitor Python 端 storage.py:RendezvousHash 的算法一致性校验：
  //   Python: int(hashlib.sha1(f"{node}:{key}".encode()).hexdigest(), 16)
  //   Java:   new BigInteger(1, sha1(node + ":" + key))
  // 两者均使用标准 SHA-1 算法，且都将 20 字节摘要解释为正整数，因此数值必然一致。
  // 本测试通过两个已知向量验证 SHA-1 与 BigInteger 路径。

  @Nested
  class CrossLanguageConsistencyTest {

    @Test
    void sha1OfAbcMatchesKnownTestVector() throws Exception {
      // 已知 SHA-1 测试向量（来源：FIPS PUB 180-4 Appendix A.1）：
      //   sha1("abc") = a9993e364706816aba3e25717850c26c9cd0d89d
      // 验证 JDK MessageDigest 给出标准结果，从而保证 computeWeight 与
      // Python 端 hashlib.sha1().hexdigest() 产出相同的摘要字节。
      java.security.MessageDigest md = java.security.MessageDigest.getInstance("SHA-1");
      byte[] digest = md.digest("abc".getBytes(java.nio.charset.StandardCharsets.UTF_8));
      BigInteger abcHash = new BigInteger(1, digest);
      BigInteger expected = new BigInteger("a9993e364706816aba3e25717850c26c9cd0d89d", 16);
      assertThat(abcHash).isEqualTo(expected);
      assertThat(abcHash.bitLength()).isEqualTo(160);
    }

    @Test
    void computeWeightReturns160BitPositiveInteger() {
      BigInteger weight = RendezvousHash.computeWeight("node1", "key1");
      // SHA-1 摘要 20 字节（160 位）；BigInteger(1, ...) 永远非负
      // 注意：若摘要含前导零字节，bitLength() 可能 < 160（这是正常的）
      assertThat(
          weight.bitLength() <= 160)
          .withFailMessage("bitLength=" + weight.bitLength())
          .isTrue();
      assertThat(weight.bitLength() > 0).withFailMessage("weight must be non-zero").isTrue();
      assertThat(weight.signum() > 0).withFailMessage("weight must be positive").isTrue();
    }

    @Test
    void computeWeightMatchesHexParsedFromSha1Digest() {
      // 验证 new BigInteger(1, digest) == new BigInteger(sha1Hex, 16)
      // 即 Java 内部实现与「先 hex 编码再 parse」的等价性（Python 端是后者）
      String node = "node_alpha";
      String key = "123:myApp";
      try {
        java.security.MessageDigest md = java.security.MessageDigest.getInstance("SHA-1");
        byte[] digest = md.digest((node + ":" + key).getBytes(java.nio.charset.StandardCharsets.UTF_8));
        StringBuilder hex = new StringBuilder();
        for (byte b : digest) {
          hex.append(String.format("%02x", b & 0xff));
        }
        BigInteger viaHex = new BigInteger(hex.toString(), 16);
        BigInteger viaBytes = RendezvousHash.computeWeight(node, key);
        assertThat(viaBytes).isEqualTo(viaHex);
      } catch (java.security.NoSuchAlgorithmException e) {
        throw new IllegalStateException(e);
      }
    }
  }

  // ────────────────────────── 与 NamingPolicy 端到端集成 ────────

  @Nested
  class NamingPolicyIntegrationTest {

    @Test
    void routesViewShardsByAppName() {
      long clusterId = 3L;
      List<String> viewNodes = new ArrayList<>();
      for (int i = 1; i <= PrecalculateStorage.DEFAULT_DISPERSED_COUNT; i++) {
        viewNodes.add(
            NamingPolicy.nodeKey(clusterId, NamingPolicy.logicalTable(PrecalculateFamily.VIEW, i)));
      }
      RendezvousHash hash = new RendezvousHash(viewNodes);

      // 给定一个 bkBizId + appName，路由结果必须是 5 个 view 分表之一
      String routeKey = NamingPolicy.routeKey(123L, "myApp");
      String selected = hash.selectNode(routeKey);
      assertThat(viewNodes.contains(selected)).withFailMessage("selected=" + selected).isTrue();

      // 同一 route key 重复调用必须稳定
      assertThat(hash.selectNode(routeKey)).isEqualTo(selected);

      // 不同 appName 通常路由到不同分表（允许少数碰巧同分表）
      String selected2 = hash.selectNode(NamingPolicy.routeKey(123L, "anotherApp"));
      // 这里只验证「不抛异常 + 属于节点集合」，不强求不同分表
      assertThat(viewNodes.contains(selected2)).isTrue();
    }

    @Test
    void routesSessionShardsByAppName() {
      long clusterId = 7L;
      List<String> sessionNodes = new ArrayList<>();
      for (int i = 1; i <= PrecalculateStorage.DEFAULT_DISPERSED_COUNT; i++) {
        sessionNodes.add(
            NamingPolicy.nodeKey(
                clusterId, NamingPolicy.logicalTable(PrecalculateFamily.SESSION, i)));
      }
      RendezvousHash hash = new RendezvousHash(sessionNodes);

      String routeKey = NamingPolicy.routeKey(999L, "sessionApp");
      String selected = hash.selectNode(routeKey);
      assertThat(sessionNodes.contains(selected)).isTrue();
    }

  }
}
