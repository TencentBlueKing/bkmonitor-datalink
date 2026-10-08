# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""KAC 屏蔽入口/处理器源码对照；查询、目标解析、快照与任务出口是显式端口夹具。"""

import ast
import copy
import datetime
import json
import os
import pathlib
import sys
import types
import typing

from kac_policy_behavior_comparison import define, load_sources, MAX_BYTES

PINS = {
    "src/kingeye/kac/alarm_shield/shield.py": "99b89610593910bf90e8f18e37fac1db6e0fc7dd1e358251f0ae7869f133418f",
    "src/kingeye/kac/common/policy_utils.py": "dd45ed3ae430569de0888323a2c5b9038ff59f2c1bc4142dd05f4560732ae65d",
    "src/kingeye/kac/kac_modules/alarm/policy/service/policy_activate_time_service.py": "776e6140a5228aa8d021cd9874403b0f116d7f01d2689404c76227464c2b396e",
    "src/kingeye/kac/kac_modules/alarm/policy/constant/policy_activate_time_constant.py": "1a7fda142cb17ec40a74e654c3ab3a7163b9b54717a905fa352cf1141ad0410b",
}
BASE = datetime.datetime(2026, 9, 30, tzinfo=datetime.UTC)


def execute(case, trees):
    source, policy_source, time_source, constants_source = PINS
    constants = {}
    define([n for n in trees[constants_source].body if isinstance(n, ast.Assign)], constants, constants_source)
    model = case.get("model", "cw-Host")
    instance = case.get("instance", "101")
    target = {"model_id": model, "model_inst_id": instance, "entity_uid": model + "|" + instance}
    child = {"id": "child", "at": 0, "title": "child", "model": model, "instance": instance, **case.get("child", {})}
    rows = []
    for main in case.get("mains", []):
        rows.append({"alarm_id": main["id"], "event_id": "event-" + main["id"], "name": "main", "alarm_time": (BASE + datetime.timedelta(seconds=main["at"])).strftime("%Y-%m-%d %H:%M:%S"), "status": main["status"], **target})
    child_row = {"alarm_id": child["id"], "event_id": "event-" + child["id"], "name": child["title"], "alarm_time": (BASE + datetime.timedelta(seconds=child["at"])).strftime("%Y-%m-%d %H:%M:%S"), "status": "received", "model_id": child["model"], "model_inst_id": child["instance"]}
    rows.append(child_row)
    calls, associations, removed = [], {}, []
    def matching(_model, ids, expression, **kwargs):
        extra = kwargs.get("extra_params") or {}
        candidates = [r for r in rows if not ids or r["alarm_id"] in ids]
        if "status__in" in extra:
            candidates = [r for r in candidates if r["status"] in extra["status__in"]]
        else:
            candidates = [r for r in candidates if r["status"] in {"received", "pending_merge"}]
        # 此处是已核对 policy2dsl canonical target AND 过滤的查询端口；不模拟通用 ES 条件匹配。
        targets = kwargs.get("target_entities")
        if targets is not None:
            candidates = [r for r in candidates if any(r["model_id"] == v["model_id"] and r["model_inst_id"] == v["model_inst_id"] for v in targets)]
        candidates = [r for r in candidates if r["name"] == expression["A"]["target_value"]]
        if "alarm_time__range" in extra:
            start, end = extra["alarm_time__range"]
            candidates = [r for r in candidates if start <= r["alarm_time"] <= end]
        if "B" in expression:
            relation = expression["B"]
            if relation.get("__origin_obj_id") != "switch" or relation.get("__origin_inst_id") != "sw1":
                raise ValueError("CMDB relation origin not derived from selected main")
            candidates = [r for r in candidates if r["model_id"] == "cw-Host" and r["model_inst_id"] == "101"]
        if kwargs.get("sort"):
            candidates.sort(key=lambda r: r["alarm_time"], reverse=kwargs["sort"].startswith("-"))
        if kwargs.get("page_size"):
            candidates = candidates[:kwargs["page_size"]]
        calls.append({"ids": sorted(ids), "target_entities": targets, "extra_params": extra, "matched": [r["alarm_id"] for r in candidates], "relation": expression.get("B")})
        return copy.deepcopy(candidates)
    def record(_records, relations):
        associations.update(relations)
    def deserialize(rules, _type):
        defaults = {"period": "once", "open_clock_time": "00:00:00", "close_clock_time": "23:59:59", "day_for_week": "*", "day_for_month": "*"}
        return [types.SimpleNamespace(**{**defaults, **r}) for r in rules]
    logger = types.SimpleNamespace(info=lambda *a, **k: None, warning=lambda *a, **k: None)
    shield_model = types.SimpleNamespace(TIME_SHIELD="time_shield", RELY_SHIELD="rely_shield", CMDB_SHIELD="cmdb_shield")
    module = types.ModuleType("kingeye.kac.alarm_shield.models"); module.AlarmShield = shield_model
    sys.modules[module.__name__] = module
    policy = {"id": "dependency", "name": "comparison", "bk_tenant_id": "tenant", "space_code": "bkcc__2", "model_id": model, "target_descriptor": {}, "is_enable": case.get("enabled", True), "shield_type": case["kind"], "shield_mode": "custom_shield", "time_range_before": 5, "time_range_after": 10, "policy": {"expression": "A", "A": {"target_value": "child" if case["kind"] == "time_shield" else "main"}}, "rely_policy": {"expression": "A", "A": {"target_value": "child"}}, "activate_times": []}
    if case["kind"] == "time_shield":
        policy["activate_times"] = [{"period": "once", "open_datetime_once": "2026-09-30 00:00:00", "close_datetime_once": "2026-09-30 00:00:10"}]
    if case.get("mode") == "cmdb_shield":
        policy["shield_mode"] = "cmdb_shield"
        policy["rely_policy"]["expression"] = "A AND B"
        policy["rely_policy"]["B"] = {"condition": "term", "target_key": "model_id", "target_value": "cw-Host", "bk_obj_asst_id": "switch_connect_host"}
    ns = {"copy": copy, "datetime": datetime, "gettext": lambda x: x, "constants": types.SimpleNamespace(**{k: v for k, v in constants.items() if k.isupper()}), "logger": logger,
        "onemodel": types.SimpleNamespace(normalize_cmdb_bk_obj_id=lambda model_id, **kw: {"cw-Switch": "switch", "cw-Host": "host"}.get(model_id)),
        "AlarmShield": shield_model, "AlarmEvent": types.SimpleNamespace(RECEIVED="received", PENDING_MERGE="pending_merge", NOT_CLOSE_ACTIVE_STATUS_LIST=["active"]), "AlarmLifeCycle": types.SimpleNamespace(SHIELDED="shielded"), "PolicyType": types.SimpleNamespace(SHIELD="shield"), "STAGE_NAME_SHIELD": "shield",
        "get_current_dt": lambda: BASE + datetime.timedelta(seconds=case.get("evaluate_at", 0)), "List": typing.List, "PolicyActivateTime": types.SimpleNamespace, "JsonMapper": types.SimpleNamespace(deserialize=deserialize),
        "get_license_module_effectiveness": lambda _: True, "ModuleCode": types.SimpleNamespace(ALARM_SHIELD="shield"), "resolve_alarm_target_entities": lambda *a, **k: [target], "PolicySnapshotService": types.SimpleNamespace(easy_query_cache=lambda *a: {"snapshot_id": "snapshot"}),
        "AUTO_CONVERGENCE_CACHE_KEY": "comparison-auto", "get_redis_connection": lambda: types.SimpleNamespace(hdel=lambda key, *values: removed.extend(values)), "handle_shield_alarms_task": types.SimpleNamespace(delay=record),
    }
    time_class = next(n for n in trees[time_source].body if isinstance(n, ast.ClassDef) and n.name == "_PolicyActivateTimeService")
    define([n for n in time_class.body if isinstance(n, ast.FunctionDef) and n.name == "is_active"], ns, time_source)
    ns["PolicyActivateTimeService"] = types.SimpleNamespace(is_active=lambda rules: ns["is_active"](None, rules))
    manage_class = next(n for n in trees[policy_source].body if isinstance(n, ast.ClassDef) and n.name == "PolicyManage")
    nodes = [copy.deepcopy(n) for n in manage_class.body if isinstance(n, ast.FunctionDef) and n.name in {"is_active_policy", "get_datetime_format"}]
    for node in nodes: node.decorator_list = []
    define(nodes, ns, policy_source)
    ns["PolicyManage"] = types.SimpleNamespace(alarm_policies_matching=matching, get_alarm_policies=lambda _: [policy], is_active_policy=ns["is_active_policy"], get_datetime_format=ns["get_datetime_format"])
    nodes = [n for n in trees[source].body if (isinstance(n, ast.ClassDef) and n.name in {"AlarmShieldHandler", "TimeShieldHandler", "RelyShieldHandler"}) or (isinstance(n, ast.FunctionDef) and n.name == "shield_matching") or (isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "SHIELD_TYPE_HANDLER_MAPPING" for t in n.targets))]
    define(nodes, ns, source)
    ids = [m["id"] for m in case.get("mains", []) if m["status"] == "received"] + [child["id"]]
    returned, _ = ns["shield_matching"](ids, rows)
    parent = next((key for key, value in associations.items() if child["id"] in value["alarm_ids"]), "")
    return {"id": case["id"], "result": {"shielded": child["id"] not in returned, "parent": parent}, "calls": calls, "removed_auto_ids": sorted(removed)}


def main():
    if len(sys.argv) != 2 or os.environ.get("PYTHONHASHSEED") != "0": raise ValueError("source root and fixed hash seed required")
    raw = sys.stdin.buffer.read(MAX_BYTES + 1)
    if len(raw) > MAX_BYTES: raise ValueError("input exceeds budget")
    cases = json.loads(raw)
    if not isinstance(cases, list) or not 1 <= len(cases) <= 128: raise ValueError("expected 1..128 cases")
    trees = load_sources(pathlib.Path(sys.argv[1]), PINS)
    results = [execute(case, trees) for case in cases]
    output = json.dumps({"source_sha256": PINS, "results": results}, ensure_ascii=False, sort_keys=True).encode()
    if len(output) > MAX_BYTES: raise ValueError("output exceeds budget")
    sys.stdout.buffer.write(output + b"\n")


if __name__ == "__main__":
    try: main()
    except (OSError, ValueError, KeyError, TypeError, StopIteration) as exc:
        print(f"KAC shield comparison failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from None
