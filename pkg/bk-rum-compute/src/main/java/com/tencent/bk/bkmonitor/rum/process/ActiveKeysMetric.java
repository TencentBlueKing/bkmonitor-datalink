// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.process;

import org.apache.flink.api.common.TaskInfo;
import org.apache.flink.api.common.functions.RuntimeContext;
import org.apache.flink.api.common.state.ListState;
import org.apache.flink.api.common.state.ListStateDescriptor;
import org.apache.flink.api.common.typeinfo.Types;
import org.apache.flink.api.java.tuple.Tuple2;
import org.apache.flink.metrics.Gauge;
import org.apache.flink.metrics.MetricGroup;
import org.apache.flink.runtime.state.FunctionInitializationContext;
import org.apache.flink.runtime.state.KeyGroupRange;
import org.apache.flink.runtime.state.KeyGroupRangeAssignment;

/**
 * Tracks active keyed windows per key group so the gauge remains accurate after checkpoint recovery
 * and rescaling.
 */
final class ActiveKeysMetric implements Gauge<Long> {
  static final String METRIC_NAME = "active_keys";

  private final int firstKeyGroup;
  private final int maxParallelism;
  private final long[] countsByKeyGroup;
  private final ListState<Tuple2<Integer, Long>> checkpointState;

  private long activeKeys;

  private ActiveKeysMetric(
      int firstKeyGroup,
      int maxParallelism,
      int keyGroupCount,
      ListState<Tuple2<Integer, Long>> checkpointState) {
    this.firstKeyGroup = firstKeyGroup;
    this.maxParallelism = maxParallelism;
    this.countsByKeyGroup = new long[keyGroupCount];
    this.checkpointState = checkpointState;
  }

  static ActiveKeysMetric restore(
      FunctionInitializationContext context, RuntimeContext runtimeContext, String windowScope)
      throws Exception {
    TaskInfo taskInfo = runtimeContext.getTaskInfo();
    int maxParallelism = taskInfo.getMaxNumberOfParallelSubtasks();
    KeyGroupRange keyGroupRange =
        KeyGroupRangeAssignment.computeKeyGroupRangeForOperatorIndex(
            maxParallelism,
            taskInfo.getNumberOfParallelSubtasks(),
            taskInfo.getIndexOfThisSubtask());
    ListStateDescriptor<Tuple2<Integer, Long>> descriptor =
        new ListStateDescriptor<>(
            windowScope + "-active-keys-by-key-group", Types.TUPLE(Types.INT, Types.LONG));
    ListState<Tuple2<Integer, Long>> checkpointState =
        context.getOperatorStateStore().getUnionListState(descriptor);
    ActiveKeysMetric metric =
        new ActiveKeysMetric(
            keyGroupRange.getStartKeyGroup(),
            maxParallelism,
            keyGroupRange.getNumberOfKeyGroups(),
            checkpointState);
    if (!context.isRestored()) {
      return metric;
    }

    for (Tuple2<Integer, Long> entry : checkpointState.get()) {
      if (!keyGroupRange.contains(entry.f0)) {
        continue;
      }
      int index = entry.f0 - metric.firstKeyGroup;
      metric.countsByKeyGroup[index] += entry.f1;
      metric.activeKeys += entry.f1;
    }
    return metric;
  }

  void register(MetricGroup metricGroup) {
    metricGroup.gauge(METRIC_NAME, this);
  }

  void keyOpened(String key) {
    int index = indexForKey(key);
    countsByKeyGroup[index]++;
    activeKeys++;
  }

  void keyClosed(String key) {
    int index = indexForKey(key);
    countsByKeyGroup[index]--;
    activeKeys--;
  }

  void snapshot() throws Exception {
    checkpointState.clear();
    for (int index = 0; index < countsByKeyGroup.length; index++) {
      long count = countsByKeyGroup[index];
      if (count != 0L) {
        checkpointState.add(Tuple2.of(firstKeyGroup + index, count));
      }
    }
  }

  @Override
  public Long getValue() {
    return activeKeys;
  }

  private int indexForKey(String key) {
    return KeyGroupRangeAssignment.assignToKeyGroup(key, maxParallelism) - firstKeyGroup;
  }
}
