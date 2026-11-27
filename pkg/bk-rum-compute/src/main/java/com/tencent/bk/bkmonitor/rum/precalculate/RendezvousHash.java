// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.precalculate;

import java.math.BigInteger;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.List;
import java.util.Objects;

/**
 * Rendezvous（最高权重）哈希：给定节点列表与 route key，返回权重最大的节点。
 *
 * <p>对应 BK-Monitor Python 端 {@code bkmonitor/apm/core/discover/precalculation/storage.py:RendezvousHash}。算法
 * 必须与 Python 端一字不差，否则同一 route key 会路由到不同分表，破坏数据连续性：
 *
 * <pre>{@code
 * for each node in nodes:
 *     weight = int(sha1("{node}:{key}").hexdigest(), 16)   # Python 端
 *     # Java 端:
 *     weight = new BigInteger(1, sha1("{node}:{key}"))      # SHA-1 摘要当无符号正整数
 *     keep node with max weight
 * }</pre>
 *
 * <p>配合 {@link NamingPolicy}：
 *
 * <pre>{@code
 * List<String> nodes = IntStream.rangeClosed(1, 5)
 *     .mapToObj(i -> NamingPolicy.nodeKey(clusterId, NamingPolicy.logicalTable(PrecalculateFamily.VIEW, i)))
 *     .collect(Collectors.toList());
 * RendezvousHash hash = new RendezvousHash(nodes);
 * String selected = hash.selectNode(NamingPolicy.routeKey(bkBizId, appName));
 * }</pre>
 *
 * @see NamingPolicy#nodeKey(long, String)
 * @see NamingPolicy#routeKey(long, String)
 * @see <a href="../../../../../../docs/index-naming.md">docs/index-naming.md §2.4 路由</a>
 */
public final class RendezvousHash {

  private final List<String> nodes;

  /**
   * @param nodes 节点列表（必须非空），顺序不影响结果
   * @throws IllegalArgumentException 当 nodes 为 null 或空
   */
  public RendezvousHash(List<String> nodes) {
    if (nodes == null || nodes.isEmpty()) {
      throw new IllegalArgumentException("nodes must not be empty");
    }
    this.nodes = List.copyOf(nodes);
  }

  /** 节点数量。 */
  public int nodeCount() {
    return nodes.size();
  }

  /**
   * 计算指定 route key 应路由到哪个节点。
   *
   * <p>对应 Python 端 {@code select_node(key)}。
   *
   * @param routeKey 路由 key（{@link NamingPolicy#routeKey(long, String)} 或
   *     {@link NamingPolicy#sharedRouteKey(String)}）
   * @return 权重最大的节点字符串；多个节点权重相同时返回遍历顺序中最先出现的那一个
   * @throws IllegalArgumentException 当 routeKey 为 null
   */
  public String selectNode(String routeKey) {
    if (routeKey == null) {
      throw new IllegalArgumentException("routeKey must not be null");
    }
    String maxNode = null;
    BigInteger maxWeight = null;
    for (String node : nodes) {
      BigInteger weight = computeWeight(node, routeKey);
      if (maxWeight == null || weight.compareTo(maxWeight) > 0) {
        maxWeight = weight;
        maxNode = node;
      }
    }
    return maxNode;
  }

  /**
   * 计算 {@code node + ":" + routeKey} 的 SHA-1 摘要，返回无符号正整数形式的权重。
   *
   * <p>对应 Python 端 {@code int(hashlib.sha1(f"{node}:{key}".encode()).hexdigest(), 16)}。
   *
   * <p>package-private 仅用于测试与跨语言一致性校验。
   */
  static BigInteger computeWeight(String node, String routeKey) {
    Objects.requireNonNull(node, "node");
    Objects.requireNonNull(routeKey, "routeKey");
    try {
      MessageDigest md = MessageDigest.getInstance("SHA-1");
      byte[] digest = md.digest((node + ":" + routeKey).getBytes(StandardCharsets.UTF_8));
      // signum = 1 → 强制按无符号正整数解析（与 Python int(hex, 16) 等价）
      return new BigInteger(1, digest);
    } catch (NoSuchAlgorithmException e) {
      // SHA-1 是 JDK 必实现算法，正常情况下不会抛
      throw new IllegalStateException("SHA-1 not available", e);
    }
  }
}
