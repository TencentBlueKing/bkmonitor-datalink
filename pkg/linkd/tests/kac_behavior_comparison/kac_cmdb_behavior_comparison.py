# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""从固定 KAC 源码提取实际 CMDB 投影函数，只使用合成 API 事实，不导入应用或联网。"""

import ast
import json
import pathlib
import sys
import types

from kac_policy_behavior_comparison import define, load_sources

SOURCES = {
    "src/kingeye/base/domains/onemodel/instances.py": "ad1c4ddc9daa170b6e2da26ee147f10f165143aba23d9125b3b2c61871c269b1",
    "src/kingeye/base/domains/onemodel/resolve.py": "1a74d8ca43650260d6add929fa78cd5239598508fe1b04815b37410f39467f48",
    "src/kingeye/base/instance_storage_topology.py": "f7bbca5f442e4f34e5da94fa4e3b18db34f0c0dceddfb126a85a6f0ad0676cd0",
}


def main():
    trees = load_sources(pathlib.Path(sys.argv[1]), SOURCES)
    raw = sys.stdin.buffer.read((1 << 20) + 1)
    if len(raw) > 1 << 20:
        raise ValueError("input budget exceeded")
    case = json.loads(raw)
    tenant, biz = "tenant", 2

    def details(*args):
        if args[:2] != (tenant, biz):
            raise ValueError("wrong KAC service scope")
        return [row for row in case["services"] if row["id"] in args[2]]

    def hosts(**kwargs):
        if kwargs["bk_tenant_id"] != tenant or kwargs["bk_biz_id"] != biz:
            raise ValueError("wrong KAC host scope")
        return [row for row in case["hosts"] if row["bk_host_id"] in kwargs["bk_host_ids"]]

    def entity(**kwargs):
        # Entity 构造端口只保留这次对照的实际函数传参，避免导入 Django；不重写投影规则。
        return {key: kwargs[key] for key in ("model_id", "model_inst_id", "display_name", "bk_biz_ids", "attributes", "source")}

    namespace = {
        "api": types.SimpleNamespace(cmdb=types.SimpleNamespace(list_service_instances_by_ids=details, get_host_by_ip=hosts)),
        "build_entity": entity,
        "BuiltinObjectModelCode": types.SimpleNamespace(CMDB_SERVICE_INSTANCE="cw-CCServiceInstance", HOST="cw-Host"),
        "DATA_PLANE_CMDB_SERVICE_INSTANCE_DIRECT": "cmdb_service_instance_direct",
        "SOURCE_CMDB_DIRECT": "cmdb_direct",
        "_shared": types.SimpleNamespace(BKCC_SPACE_PREFIX="bkcc__"),
    }
    functions = {"get_cmdb_service_instance_entities_by_ids", "_direct_host_to_entity", "_topology_unique_id"}
    for path, tree in trees.items():
        define([node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name in functions], namespace, path)
    services = namespace["get_cmdb_service_instance_entities_by_ids"](case["ids"], bk_tenant_id=tenant, bk_biz_id=biz)
    result = {
        "source_sha256": SOURCES,
        "services": services,
        "hosts": [namespace["_direct_host_to_entity"](row, bk_biz_id=biz) for row in case["hosts"]],
        "locator": namespace["_topology_unique_id"](tenant, "cw-Module", 8),
    }
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
