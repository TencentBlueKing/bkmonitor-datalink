# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""Replay raw/check around known storage transitions on three explicit query paths."""
import argparse
from collections import Counter
import json
import subprocess
from concurrent.futures import ThreadPoolExecutor

parser = argparse.ArgumentParser(description=__doc__)
for opt in ("env", "tenant", "target", "baseline-target", "table"):
    parser.add_argument("--" + opt, required=True)
parser.add_argument("--second-table", help="Second RT for a concurrent ES/Doris raw request")
parser.add_argument("--biz", type=int, required=True)
parser.add_argument("--window", action="append", required=True, help="CASE:START:END (Unix seconds)")
args = parser.parse_args()


def request(target, direct, op, start, end):
    body = dict(bk_biz_id=args.biz, uq_target=target, doris_direct_query=direct,
                start_time=start, end_time=end, limit=3, order_by=["-dtEventTimeStamp"],
                query_list=[dict(data_source="bklog", table_id=args.table, reference_name="a", limit=3)])
    if args.second_table:
        body["query_list"].append(dict(body["query_list"][0], table_id=args.second_table, reference_name="b"))
    proc = subprocess.run(["bkmonitor-admin", "api", "call", "unify_query." + op, "--env", args.env,
                           "--tenant", args.tenant, "--json", "--body-file", "-"], text=True,
                          input=json.dumps(body), capture_output=True, timeout=90)
    response = json.loads(proc.stdout or proc.stderr)
    if "data" not in response:
        raise ValueError(str(response.get("error", response))[:350])
    return response["data"]


def leg(target, direct, start, end):
    result = {}
    for op in ("check", "raw"):
        try:
            data = request(target, direct, op, start, end)
            result[op + "_trace"] = data.get("trace_id")
            if op == "check":
                result["routes"] = [dict(backend=r.get("query_backend") or r.get("storage_type"),
                                         cluster=r.get("storage_id"), physical=r.get("physical_tables"))
                                    for r in data["route_info"]]
            else:
                result["rows"] = data["list"] or []
                result["count"] = len(result["rows"])
                result["sources"] = dict(Counter(r.get("__result_table", "unknown") for r in result["rows"]))
                result["sort_values"] = [r.get("dtEventTimeStamp") for r in result["rows"]]
                result["total"] = data.get("total")
                result["targets"] = list((data.get("result_table_options") or {}).keys())
                result["partial"] = data.get("is_partial")
                result["status"] = data.get("status")
        except Exception as exc:
            result[op + "_error"] = str(exc)
    return result


for window in args.window:
    name, start, end = window.split(":")
    paths = [(args.baseline_target, False), (args.target, False), (args.target, True)]
    with ThreadPoolExecutor(max_workers=3) as pool:
        legs = list(pool.map(lambda p: leg(*p, start, end), paths))
    rows = [r.pop("rows", None) for r in legs]
    def same(i, j):
        if rows[i] is None or rows[j] is None:
            return None
        return (rows[i] == rows[j] and all(legs[i][k] == legs[j][k] for k in ("total", "status", "partial")))
    print(json.dumps(dict(case=name, table=args.table, second_table=args.second_table, start=start, end=end, legs=legs,
                          version_equal=same(0, 1), backend_equal=same(1, 2))), flush=True)
