# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""只读执行固定 KAC 源码的确定性规则，不启动 Django 或连接 KAC 服务。

实际函数体由 AST 提取，文件摘要先校验。时钟、动态配置、UUID 和导入的基础字段目录
是显式夹具；此入口不代表 KAC 应用、查询、Redis 或 Celery 的端到端运行。
"""

import __future__
import ast
import datetime
import hashlib
import json
import os
import pathlib
import re
import sys
import types
import uuid

SOURCES = {
    "src/kingeye/kac/alarm_merge/utils.py": "2abb6aeae5ff688e915db058831f2bfa71e22bce5831d94b9cd740615e702205",
    "src/kingeye/kac/alarm_merge/merge.py": "a6226172d35e51b0f78f83d3b63a3c813527353acb97aec0ee69b6f6368015a0",
    "src/kingeye/kac/kac_modules/alarm/policy/service/policy_activate_time_service.py": "776e6140a5228aa8d021cd9874403b0f116d7f01d2689404c76227464c2b396e",
    "src/kingeye/kac/kac_modules/alarm/policy/constant/policy_activate_time_constant.py": "1a7fda142cb17ec40a74e654c3ab3a7163b9b54717a905fa352cf1141ad0410b",
    "src/kingeye/kac/common/notify_utils.py": "8cb40aeae4e764eec1e39773993c3fdd3292307a94fee27eec515746a191414c",
    "src/kingeye/kac/common/utils.py": "b7f260317c8bf6cef7fce94ca55a7b5e79545f44b63d15f69799206c4ca8d45d",
}
MAX_BYTES = 1 << 20


def load_sources(root, sources=None):
    trees = {}
    for path, expected in (SOURCES if sources is None else sources).items():
        source = root / path
        if not source.is_file() or source.stat().st_size > 8 << 20:
            raise ValueError(f"invalid source file: {path}")
        raw = source.read_bytes()
        if hashlib.sha256(raw).hexdigest() != expected:
            raise ValueError(f"source snapshot mismatch: {path}")
        trees[path] = ast.parse(raw, filename=path)
    return trees


def define(nodes, namespace, filename):
    # 只移除模块导入/初始化；选中函数的业务函数体保持源码原样。
    tree = ast.Module(body=nodes, type_ignores=[])
    exec(compile(tree, filename, "exec", flags=__future__.annotations.compiler_flag), namespace)


def main():
    if len(sys.argv) != 2:
        raise ValueError("expected explicit KAC source root")
    if os.environ.get("PYTHONHASHSEED") != "0":
        raise ValueError("PYTHONHASHSEED must be fixed to 0")
    raw = sys.stdin.buffer.read(MAX_BYTES + 1)
    if len(raw) > MAX_BYTES:
        raise ValueError("input exceeds 1 MiB")
    cases = json.loads(raw)
    if not isinstance(cases, list) or not 1 <= len(cases) <= 128:
        raise ValueError("expected 1..128 cases")
    trees = load_sources(pathlib.Path(sys.argv[1]))
    utils_path, merge_path, time_path, constants_path, pattern_path, common_path = SOURCES
    constants = {}
    define([n for n in trees[constants_path].body if isinstance(n, ast.Assign)], constants, constants_path)
    patterns = {"re": re}
    define([n for n in trees[pattern_path].body if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "ALARM_FIELD_MATCH_PATTEN" for t in n.targets)], patterns, pattern_path)
    clock = datetime.datetime(2026, 9, 30, 10, 0, 0)
    config = {}
    namespace = {
        "get_current_dt": lambda: clock,
        "get_dynamic_config": lambda: types.SimpleNamespace(domains=types.SimpleNamespace(kac=types.SimpleNamespace(strategy=types.SimpleNamespace(alarm_merge_field=config)))),
        "constants": types.SimpleNamespace(**{k: v for k, v in constants.items() if k.isupper()}),
        "ALARM_FIELD_MATCH_PATTEN": patterns["ALARM_FIELD_MATCH_PATTEN"],
        "uuid": types.SimpleNamespace(uuid4=lambda: uuid.UUID("00000000-0000-4000-8000-000000000001")),
        "json": json,
        "hashlib": hashlib,
        "DEFAULT_TENANT_ID": "comparison-tenant",
        "MERGE_SOURCE_ID": "comparison-merge-source",
        "MERGE_SOURCE_NAME": "comparison-merge-name",
        "MERGE_CACHE_KEY": "comparison-members",
        "MERGE_WINDOW_CACHE_KEY": "comparison-window",
        "AlarmEvent": types.SimpleNamespace(FIRING="firing"),
    }
    base_module = types.ModuleType("kingeye.kac.alarm_collect.clients.custom.baseclient")
    # 默认补空字段不参与本组断言；只提供最小依赖目录，不导入实际接入模块。
    base_module.BaseClient = types.SimpleNamespace(ALARM_BASE_KEY_DICT={})
    sys.modules[base_module.__name__] = base_module
    define([n for n in trees[common_path].body if isinstance(n, ast.FunctionDef) and n.name == "md5_hash"], namespace, common_path)
    define([n for n in trees[utils_path].body if isinstance(n, ast.FunctionDef) and n.name == "generate_merge_alarm"], namespace, utils_path)
    define([n for n in trees[merge_path].body if isinstance(n, ast.FunctionDef) and n.name == "build_cache_keys"], namespace, merge_path)
    service = next(n for n in trees[time_path].body if isinstance(n, ast.ClassDef) and n.name == "_PolicyActivateTimeService")
    define([n for n in service.body if isinstance(n, ast.FunctionDef) and n.name == "is_active"], namespace, time_path)
    results = []
    seen = set()
    for case in cases:
        name, kind = case["id"], case["kind"]
        if not isinstance(name, str) or not name or name in seen:
            raise ValueError("invalid or duplicate case identity")
        seen.add(name)
        if kind == "schedule":
            clock = datetime.datetime.fromisoformat(case["at"])
            rules = []
            for rule in case["rules"]:
                defaults = {"period": "", "open_datetime_once": "", "close_datetime_once": "", "open_clock_time": "00:00:00", "close_clock_time": "23:59:59", "day_for_week": "*", "day_for_month": "*"}
                defaults.update(rule)
                rules.append(types.SimpleNamespace(**defaults))
            result = {"active": namespace["is_active"](None, rules)}
        elif kind == "group":
            fields = [f"f{i}" for i in range(len(case["values"]))]
            alarm = {"alarm_id": "a", **dict(zip(fields, case["values"]))}
            cache, window = namespace["build_cache_keys"](alarm, fields, "snapshot", "condition", {}, "comparison")
            result = {"valid": cache is not None, "cache_key": cache, "window_key": window}
        elif kind == "template":
            config = {"max_merge_field_length": case.get("limit", 500)}
            alarms = [{"event_id": f"e{i}", "bk_tenant_id": "comparison-tenant", **row} for i, row in enumerate(case["members"])]
            policy = {"id": "comparison", "bk_tenant_id": "comparison-tenant", "aggregate_fields": case.get("fields", []), "new_alarm_config": [{"key": "name", "value": "merged ${alarm_num}"}, {"key": "level", "value": "warning"}, {"key": "content", "value": case["template"]}]}
            alarm = namespace["generate_merge_alarm"](policy, alarms)
            result = {"title": alarm["name"], "content": alarm["content"]}
        else:
            raise ValueError("unsupported case kind")
        results.append({"id": name, "kind": kind, "result": result})
    runtime = {"implementation": sys.implementation.name, "version": sys.version, "hash_seed": "0", "hash_algorithm": sys.hash_info.algorithm}
    output = json.dumps({"source_sha256": SOURCES, "runtime": runtime, "results": results}, ensure_ascii=False, sort_keys=True).encode()
    if len(output) > MAX_BYTES:
        raise ValueError("output exceeds 1 MiB")
    sys.stdout.buffer.write(output + b"\n")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError, StopIteration) as exc:
        print(f"KAC comparison failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from None
