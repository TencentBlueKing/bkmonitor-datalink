# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""执行 KAC 实际防抖/聚合/合并裁决函数；存储、候选查询和任务队列使用显式序列夹具。"""

import collections
import contextlib
import copy
import datetime
import fnmatch
import hashlib
import itertools
import json
import os
import pathlib
import sys
import time
import types
import ast

from kac_policy_behavior_comparison import define, load_sources, MAX_BYTES

PINS = {
    "src/kingeye/kac/alarm_converge/convergence.py": "2d34374f4b5a4378139914af3f3736121a90161b92436b9fd699ab2b00a583a5",
    "src/kingeye/kac/alarm_merge/merge.py": "a6226172d35e51b0f78f83d3b63a3c813527353acb97aec0ee69b6f6368015a0",
    "src/kingeye/kac/alarm_merge/celery_tasks.py": "574cf467b36c41f2840c64cfd32ffd8ad88ae22bef9020be211755cc45b635b3",
    "src/kingeye/kac/common/utils.py": "b7f260317c8bf6cef7fce94ca55a7b5e79545f44b63d15f69799206c4ca8d45d",
}
BASE_TIME = 1790726400


class RedisFixture:
    """只实现选中函数调用的 Redis 命令，保留真实 bytes 返回形状，不实现锁/网络/原子性。"""

    def __init__(self):
        self.hashes, self.lists, self.sorted_sets = {}, {}, {}

    def hget(self, name, key):
        return self.hashes.get(name, {}).get(key)

    def hset(self, name, key, value):
        self.hashes.setdefault(name, {})[key] = str(value).encode()

    def hdel(self, name, *keys):
        for key in keys:
            self.hashes.get(name, {}).pop(key, None)

    def zadd(self, name, values):
        self.sorted_sets.setdefault(name, {}).update(values)

    def lindex(self, name, index):
        values = self.lists.get(name, [])
        return values[index] if values else None

    def rpush(self, name, value):
        self.lists.setdefault(name, []).append(str(value).encode())

    def keys(self, pattern):
        return [key.encode() for key in sorted(self.lists) if fnmatch.fnmatch(key, pattern)]

    def lrange(self, name, start, stop):
        values = self.lists.get(name.decode(), [])
        return values[start:] if stop == -1 else values[start:stop + 1]

    def zrangebyscore(self, name, start, end):
        values = self.sorted_sets.get(name, {})
        return [key.encode() for key, score in sorted(values.items(), key=lambda x: (x[1], x[0])) if start <= score <= end]

    def lrem(self, name, count, value):
        if count != 1:
            raise ValueError("fixture only supports lrem count=1")
        key = name.decode()
        values = self.lists.get(key, [])
        values.remove(str(value).encode())
        if not values:
            self.lists.pop(key, None)

    def delete(self, name):
        self.lists.pop(name.decode(), None)


def execute(case, trees):
    redis = RedisFixture()
    now = BASE_TIME
    records, successes, failures = {}, [], []
    logger = types.SimpleNamespace(info=lambda *a, **k: None, warning=lambda *a, **k: None, error=lambda *a, **k: None)
    clock = types.SimpleNamespace(time=lambda: now, mktime=time.mktime, strptime=time.strptime)
    query = lambda query, **kwargs: [copy.deepcopy(records[key]) for key in query.get("alarm_id", []) if key in records and ("status" not in query or records[key]["status"] in query["status"])]
    policy = {"id": "policy", "name": "comparison", "bk_tenant_id": "tenant", "merge_cycle": 60, "is_cycle_merge": case.get("cyclic", False), "policy": ["g0", "g1"]}
    snapshot = types.SimpleNamespace(policy_content=policy)
    def success(**kwargs):
        successes.append(sorted(kwargs["args"][2]))
    def release(**kwargs):
        failures.append(sorted(alarm["alarm_id"] for alarm in kwargs["kwargs"]["merge_release_alarms"]))
    ns = {
        "copy": copy, "json": json, "hashlib": hashlib, "defaultdict": collections.defaultdict, "chain": itertools.chain,
        "time": clock, "logger": logger, "gettext": lambda text: text,
        "AlarmConvergence": types.SimpleNamespace(PEAK_CLIPPING_SCHEME_TYPE="clip", DURATION_TYPE_NAME={"second": "秒"}),
        "AlarmEvent": types.SimpleNamespace(query_alarms=query, CLOSE_STATUS_SET={"recovered", "closed"}, RECEIVED="received", PENDING_MERGE="pending_merge"),
        "AUTO_CONVERGENCE_CACHE_KEY": "comparison-auto", "CONVERGENCE_POLICY_PEAK_CLIPPING_CACHE_KEY": "comparison-clip", "CONVERGENCE_POLICY_AGGREGATION_CACHE_KEY": "comparison-aggregation",
        "MERGE_CACHE_KEY": "comparison-members", "MERGE_WINDOW_CACHE_KEY": "comparison-window", "DEFAULT_TENANT_ID": "tenant", "CELERY_TENANT_HEADER": "bk_tenant_id",
        "get_redis_connection": lambda: redis,
        "PolicySnapshotService": types.SimpleNamespace(find=lambda _: snapshot), "PolicySnapshot": lambda **kw: kw,
        "PolicyType": types.SimpleNamespace(MERGE="merge"), "PolicySnapshotNotExist": KeyError,
        "handle_alarm_merge": types.SimpleNamespace(apply_async=success), "alarm_process": types.SimpleNamespace(apply_async=release), "tenant_context": lambda _: contextlib.nullcontext(),
    }
    functions = {"md5_hash", "str_date2timestamp", "peak_clipping_convergence", "_aggregation_converge_calculation", "update_redis_cache", "alarm_merge_calc"}
    for path, tree in trees.items():
        nodes = [copy.deepcopy(n) for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in functions]
        # 调度与锁装饰器是此试验的端口；裁决函数体保持原样。
        for node in nodes:
            node.decorator_list = []
        define(nodes, ns, path)
    outputs = []
    aggregate_cache = {}
    for step in case["steps"]:
        now = BASE_TIME + step["at"]
        if case["kind"] == "clip":
            alarms = {key: {"alarm_id": key, "event_id": "fingerprint", "alarm_time": datetime.datetime.fromtimestamp(BASE_TIME + step.get("event_at", step["at"]), datetime.UTC).strftime("%Y-%m-%d %H:%M:%S")} for key in step["ids"]}
            received, matching = set(alarms), set(alarms)
            clip_policy = {"id": "policy", "name": "comparison", "scheme": {"clip": {"duration": 60, "duration_type": "second", "real_duration": 60, "count": 3}}}
            ns["peak_clipping_convergence"](alarms, matching, received, {}, clip_policy, now, redis)
            state = json.loads(redis.hget("comparison-clip", "policy"))["fingerprint"]
            outputs.append({"at": step["at"], "count": state["current_count"], "admitted": sorted(received)})
        elif case["kind"] == "aggregation":
            for key in step.get("closed", []):
                records[key]["status"] = "closed"
            key = step["ids"][0]
            records[key] = {"alarm_id": key, "status": "active"}
            new_cache, suppressed, _ = ns["_aggregation_converge_calculation"]("policy", redis, 60, {"group": step["ids"]}, copy.deepcopy(aggregate_cache))
            aggregate_cache = new_cache
            outputs.append({"at": step["at"], "owner": aggregate_cache["group"]["alarm_id"], "admitted": sorted(set(step["ids"]) - set(suppressed))})
        elif case["kind"] == "merge":
            for alarm in step.get("add", []):
                key = alarm["id"]
                records[key] = {"alarm_id": key, "bk_tenant_id": "tenant", "status": "pending_merge"}
                for group in alarm["groups"]:
                    cache_key = "comparison-members:1:" + ns["md5_hash"](policy["policy"][group])
                    ns["update_redis_cache"](redis, cache_key, "comparison-window:1", key, now, 60, case.get("cyclic", False))
            if step.get("judge"):
                successes.clear(); failures.clear()
                ns["alarm_merge_calc"]()
                outcome, members = ("succeeded", successes[0]) if successes else (("failed", failures[0]) if failures else ("waiting", []))
                outputs.append({"at": step["at"], "outcome": outcome, "members": members})
        else:
            raise ValueError("unsupported state case kind")
    return {"id": case["id"], "kind": case["kind"], "steps": outputs}


def main():
    if len(sys.argv) != 2 or os.environ.get("PYTHONHASHSEED") != "0" or os.environ.get("TZ") != "UTC":
        raise ValueError("expected source root, PYTHONHASHSEED=0 and TZ=UTC")
    time.tzset()
    raw = sys.stdin.buffer.read(MAX_BYTES + 1)
    if len(raw) > MAX_BYTES:
        raise ValueError("state input exceeds 1 MiB")
    cases = json.loads(raw)
    if not isinstance(cases, list) or not 1 <= len(cases) <= 128:
        raise ValueError("expected 1..128 state cases")
    trees = load_sources(pathlib.Path(sys.argv[1]), PINS)
    results = [execute(case, trees) for case in cases]
    output = json.dumps({"source_sha256": PINS, "runtime": {"version": sys.version, "hash_seed": "0", "timezone": "UTC"}, "results": results}, ensure_ascii=False, sort_keys=True).encode()
    if len(output) > MAX_BYTES:
        raise ValueError("state output exceeds 1 MiB")
    sys.stdout.buffer.write(output + b"\n")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError) as exc:
        print(f"KAC state comparison failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from None
