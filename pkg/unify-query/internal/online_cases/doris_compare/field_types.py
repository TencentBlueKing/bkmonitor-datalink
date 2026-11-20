# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""Compare physical column declarations and typed JSON values for a bounded raw page."""
import argparse
import json
import subprocess
from decimal import Decimal


def call(args, operation, body):
    p = subprocess.run(["bkmonitor-admin", "api", "call", operation, "--env", args.env,
                        "--tenant", args.tenant, "--json", "--body-file", "-"],
                       input=json.dumps(body), capture_output=True, text=True, timeout=90)
    response = json.loads(p.stdout or p.stderr, parse_float=Decimal)
    if p.returncode or "data" not in response:
        raise ValueError(str(response.get("error", "API error"))[:240])
    return response["data"]


def typed(value):
    # Python normally treats True == 1 == 1.0. Retain the JSON representation recursively.
    if isinstance(value, dict):
        return ("object", {k: typed(v) for k, v in value.items()})
    if isinstance(value, list):
        return ("array", [typed(v) for v in value])
    return (type(value).__name__, value)


def verify(args, table):
    meta = call(args, "doris_storage.physical_metadata", {"table_id": table})
    cols = {c["COLUMN_NAME"]: c["COLUMN_TYPE"]
            for c in (meta.get("physical_metadata") or {}).get("columns", [])}
    query = {"table_id": table, "data_source": "bklog", "reference_name": "a", "limit": args.limit}
    body = dict(bk_biz_id=args.biz, uq_target=args.target, start_time=args.start,
                end_time=args.end, query_list=[query], limit=args.limit,
                order_by=["-dtEventTimeStamp", "-__unique_key__"], is_search_after=True)
    baseline, direct = [call(args, "unify_query.raw", dict(body, doris_direct_query=f)) for f in (False, True)]
    rows = [d.get("list") or [] for d in (baseline, direct)]
    traces = [d.get("trace_id") for d in (baseline, direct)]
    keys = [list((d.get("result_table_options") or {}).keys()) for d in (baseline, direct)]
    routed = all(keys) and all("|doris|" not in k for k in keys[0]) and all("|doris|" in k for k in keys[1])
    aligned = bool(rows[0]) and all("__unique_key__" in r for page in rows for r in page) and (
        [r["__unique_key__"] for r in rows[0]] == [r["__unique_key__"] for r in rows[1]])
    result = dict(table=table, traces=traces, counts=list(map(len, rows)), routed=routed,
                  aligned=aligned, ddl=bool(cols), columns=[])
    for key in sorted(set(cols).union(*(r.keys() for page in rows for r in page))):
        present = [r[key] for r in rows[1] if key in r]
        nonnull = [v for v in present if v is not None]
        equal = aligned and all((key in a) == (key in b) and typed(a.get(key)) == typed(b.get(key))
                                for a, b in zip(*rows))
        result["columns"].append(dict(field=key, physical=cols.get(key), types=sorted({typed(v)[0] for v in nonnull}),
                                      nonnull=len(nonnull), null=len(present)-len(nonnull),
                                      missing=len(rows[1])-len(present), equal=equal))
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for opt in ("env", "tenant", "target", "start", "end"):
        parser.add_argument("--" + opt, required=True)
    parser.add_argument("--biz", required=True, type=int)
    parser.add_argument("--table", required=True, action="append")
    parser.add_argument("--limit", default=10, type=int, choices=range(1, 101), metavar="1..100")
    args = parser.parse_args()
    failed = False
    for name in args.table:
        try:
            item = verify(args, name)
            failed |= not (item["routed"] and item["aligned"] and item["ddl"]
                           and all(c["equal"] for c in item["columns"]))
        except Exception as error:
            item, failed = dict(table=name, error=str(error)), True
        print(json.dumps(item, ensure_ascii=False), flush=True)
    raise SystemExit(int(failed))
