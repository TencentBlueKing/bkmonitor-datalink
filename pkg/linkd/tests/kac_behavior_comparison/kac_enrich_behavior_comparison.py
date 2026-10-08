# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""固定旧 KAC 源码的 CMDB 身份、首条选择与最终回滚对照；只使用合成实例，不导入应用。"""

import ast
import copy
import importlib.util
import json
import pathlib
import sys
import types
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor

from kac_policy_behavior_comparison import define, load_sources

SOURCES = {'src/kingeye/kac/alarm_collect/clients/enrich_utils.py': 'd697f68bc82ce90cdf680361cd2cb50874bd08d5b3334490ce8f0b995fe81f6e', 'src/kingeye/kac/common/policy_utils.py': '9ca3d3916485c9f9b38a012b97a8f64051c3b406321a5045fb1b1296e110df99'}


def evaluate(expression, leaves):
    def visit(node):
        if isinstance(node, ast.Name):
            return leaves[node.id]
        if isinstance(node, ast.BoolOp):
            values = [visit(child) for child in node.values]
            return all(values) if isinstance(node.op, ast.And) else any(values)
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
            return not visit(node.operand)
        raise ValueError("unsupported synthetic expression")
    return visit(ast.parse(expression, mode="eval").body)


class Cache:
    def hget(self, *args):
        return "{}"

    def close(self):
        pass


def main():
    root = pathlib.Path(sys.argv[1])
    trees = load_sources(root, SOURCES)
    raw = sys.stdin.buffer.read((1 << 20) + 1)
    if len(raw) > 1 << 20:
        raise ValueError("input budget exceeded")
    cases = json.loads(raw)
    spec = importlib.util.spec_from_file_location("conversion", root / "src/kingeye/kac/linkd/enrich.py")
    conversion = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(conversion)
    output = []
    for case in cases:
        def query(**kwargs):
            if case.get("query_error"):
                raise ValueError("synthetic query failure")
            if kwargs["bk_tenant_id"] != "t" or kwargs["size"] != 1:
                raise ValueError("wrong query scope or selection")
            policy = kwargs["property_filter"]
            found = []
            for row in sorted(case["instances"], key=lambda row: str(row["bk_host_id"])):
                leaves = {}
                for key, rule in policy.items():
                    if key == "expression":
                        continue
                    field = rule["target_key"].removesuffix(".keyword")
                    leaves[key] = str(row.get(field, "")) == str(rule["target_value"])
                if evaluate(policy["expression"], leaves):
                    found.append(copy.deepcopy(row))
            return len(found), found[:1]

        dynamic = types.SimpleNamespace(domains=types.SimpleNamespace(kac=types.SimpleNamespace(strategy=types.SimpleNamespace(
            multi_model_enrich={}, fields_length_constraints={"multi_model_enrich_field": {"value": 4096}}
        ))))
        env = {"copy": copy, "json": json, "re": __import__("re"), "defaultdict": defaultdict,
               "ThreadPoolExecutor": ThreadPoolExecutor, "settings": types.SimpleNamespace(THREAD_NUM=2),
               "get_tenant_id": lambda: "t", "get_tenant_cache_key": lambda key: "t:" + key,
               "get_dynamic_config": lambda: dynamic, "get_redis_connection": Cache,
               "_get_object_id_obj_mapping": lambda: {1: "host"}, "get_cmdb_id_field": lambda obj: "bk_host_id",
               "get_meta_obj_info": lambda obj: ["cw-Host", "主机"],
               "normalize_cmdb_bk_obj_id": lambda **kwargs: "host" if kwargs["model_id"] == "cw-Host" else None,
               "_policy_to_property_filter": lambda policy: policy,
               "onemodel": types.SimpleNamespace(search_instances=query),
               "pipeline_logger": types.SimpleNamespace(info=lambda *a: None, exception=lambda *a: None),
               "constants": types.SimpleNamespace(EXACT="term", CMDB_OBJECT_ATTRIBUTE_CACHE_KEY="attributes"),
               "builtin_cmdb_enrich": lambda alarm: None,
               "AlarmLifeCycle": types.SimpleNamespace(ENRICH="enrich", bulk_create=lambda rows: None)}
        for filename, name in [("src/kingeye/kac/common/policy_utils.py", "enrich_inst_rules_to_policy"),
                               ("src/kingeye/kac/alarm_collect/clients/enrich_utils.py", "_normalize_builtin_cmdb_alarm_identity")]:
            node = next(node for node in trees[filename].body if isinstance(node, ast.FunctionDef) and node.name == name)
            define([node], env, filename)
        original = next(node for node in trees["src/kingeye/kac/alarm_collect/clients/enrich_utils.py"].body
                        if isinstance(node, ast.ClassDef) and node.name == "EnrichRuleHandler")
        methods = {"__init__", "_add_life_cycles_content", "_split_alarm", "_update_alarms_inst_id", "_get_inst_info",
                   "process_cmdb_rules", "_handle_cmdb_enrich_field", "_handle_needy_redis_enrich_field", "enrich"}
        selected = copy.deepcopy(original)
        selected.body = [node for node in selected.body if isinstance(node, ast.FunctionDef) and node.name in methods]
        exec(compile(ast.Module(body=[selected], type_ignores=[]), "<fixed KAC CMDB>", "exec"), env)
        handler_type = env["EnrichRuleHandler"]
        handler_type.is_obj_rules_match = staticmethod(lambda rules, alarm: evaluate(
            rules["expression"], {key: str(alarm.get(leaf["field"], "")) == str(leaf["value"])
                                  for key, leaf in rules.items() if key != "expression"}))
        handler_type.process_normal_rules = lambda self: None
        handler_type._validate_alarm_filed = lambda self, alarm_id: None
        handler_type._build_enrich_metadata = lambda self: {}
        alarm = {"alarm_id": "a", "source_id": "source", "bk_obj_id": "", "bk_inst_id": "", "model_id": "",
                 "model_name": "", "model_inst_id": "", "owner": "", **case["alarm"]}
        rules = copy.deepcopy(case["rules"])
        handler = handler_type([alarm], rules, [])
        alarms, _ = handler.enrich()
        models = {1: {"model_id": "cw-Host", "model_name": "主机", "bk_obj_id": "host", "attributes": {"ip": "keyword"}}}
        converted = [conversion.cmdb_rule(rule, f"cmdb_{index + 1}", models) for index, rule in enumerate(case["rules"])]
        output.append({"kac": alarms[0], "config": {"rules": converted, "rollback_unmatched": True}})
    print(json.dumps({"source_sha256": SOURCES, "results": output}, ensure_ascii=False))


if __name__ == "__main__":
    main()
