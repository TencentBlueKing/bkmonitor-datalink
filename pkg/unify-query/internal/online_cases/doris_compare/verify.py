# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""Read-only BKBase/Doris differential tests via the authenticated Admin CLI."""
import argparse
import json
import subprocess
import re
import sys

parser = argparse.ArgumentParser(description=__doc__)
for name in ("env", "tenant", "target", "table", "start", "end", "metric"):
    parser.add_argument("--" + name, required=True)
parser.add_argument("--biz", type=int, required=True)
parser.add_argument("--second-table", help="Optional second RT in the same authorized space")
parser.add_argument("--lucene", action="append", default=[], help="Additional positive query_string to compare")
parser.add_argument("--nested-field", default="__ext.container_name")
parser.add_argument("--text-field", default="log")
args = parser.parse_args()
outcomes = []
common = dict(bk_biz_id=args.biz, uq_target=args.target)
query = dict(data_source="bklog", table_id=args.table, reference_name="a", limit=3)
order = ["-dtEventTimeStamp", "-__unique_key__"]
raw = dict(start_time=args.start, end_time=args.end, query_list=[query], limit=3, order_by=order)
prom = dict(start=args.start, end=args.end, step="60s")
flags = (False, True)


def call(op, body, flag):
    params = {**common, **body}
    if flag is not None:
        params["doris_direct_query"] = flag
    proc = subprocess.run(["bkmonitor-admin", "api", "call", "unify_query." + op,
                           "--env", args.env, "--tenant", args.tenant, "--json",
                           "--body-file", "-"], input=json.dumps(params), text=True,
                          capture_output=True, timeout=90)
    response = json.loads(proc.stdout or proc.stderr)
    if proc.returncode or "error" in response or "data" not in response:
        raise ValueError(str(response.get("error", response))[:450])
    return response["data"]


def with_query(**updates):
    return {**raw, "query_list": [{**query, **updates}]}


def condition(*terms, join="and"):
    return dict(field_list=[dict(field_name=k, op=op, value=[str(v)]) for k, op, v in terms],
                condition_list=[join] * (len(terms) - 1))


def canon(series):
    return sorted(series, key=lambda s: json.dumps(s, sort_keys=True))


def run(name, op, body, *, cursor=None, scroll=False, route=False, empty=False,
        expected=None, modes=flags, validate=None):
    traces = []
    outputs = []
    errors = []
    try:
        for i, flag in enumerate(modes):
            request = dict(body)
            if cursor:
                request["result_table_options"] = cursor[i]["result_table_options"]
            try:
                data = call(op, request, flag)
            except Exception as exc:
                errors.append(f"{flag}: {exc}")
                continue
            outputs.append(data)
            traces.append(data.get("trace_id"))
        if name == "exact-total" and len(errors) == len(modes) and all("unify-query HTTP 400" in e for e in errors):
            print(json.dumps(dict(case=name, status="EXPECTED_REJECTION", detail="both HTTP 400", traces=traces)), flush=True)
            outcomes.append("EXPECTED_REJECTION")
            return None
        if errors:
            raise RuntimeError(" | ".join(errors))
        if name == "exact-total":
            raise ValueError("expected HTTP 400 on both backends")
        if route:
            backends = [[r.get("query_backend") or r.get("storage_type")
                         for r in d["route_info"]] for d in outputs]
            assert all(b and set(b) == ({"doris"} if f else {"bk_sql"})
                       for b, f in zip(backends, modes)), str(backends)
            note = str(backends)
        else:
            field = {"promql": "series", "ts": "series", "reference": "series", "field_map": "data"}.get(op, "list")
            rows = [d[field] or [] for d in outputs]
            if field == "series":
                rows = [canon(r) for r in rows]
            if scroll:
                rows = [[{k: v for k, v in r.items() if k != "__table_uuid"} for r in page] for page in rows]
            if field == "data":
                rows = [canon(r) for r in rows]
            counts = [len(r) for r in rows]
            note = f"{field}={counts}"
            if field == "series":
                samples = [sum(len(s.get("values") or []) for s in r) for r in rows]
                note += f" samples={samples}"
                if any(d.get("is_partial") for d in outputs):
                    raise ValueError("partial series")
                if not any(samples):
                    raise LookupError("both series have no samples")
            if field == "list" and not scroll:
                for i, (d, f) in enumerate(zip(outputs, modes)):
                    keys = list((d.get("result_table_options") or {}).keys())
                    if d[field]:
                        assert keys and all(("|doris|" in k) == bool(flags[i] if cursor else f) for k in keys), "unexpected backend"
            assert rows[0] == rows[1], note + " payload differs"
            if field == "list":
                assert outputs[0].get("total") == outputs[1].get("total"), "total differs"
            if empty:
                assert not rows[0], "expected no hits"
            elif not rows[0]:
                raise LookupError("both empty")
            if expected is not None:
                assert rows[0] == expected, "unexpected records"
            if validate:
                assert validate(rows[0]), "semantic assertion failed"
        status = "PASS"
    except LookupError as exc:
        status, note = "EMPTY" if type(exc) is LookupError else "ERROR", str(exc)
    except Exception as exc:
        status, note = "FAIL", str(exc)
    print(json.dumps(dict(case=name, status=status, detail=note, traces=traces), ensure_ascii=False), flush=True)
    outcomes.append(status)
    return outputs if status == "PASS" else None


run("route", "check", raw, route=True)
run("default", "raw", raw, modes=(None, False))
base = run("raw", "raw", raw)
if not base:
    raise SystemExit("raw baseline unavailable")
run("field-map", "field_map", dict(start_time=args.start, end_time=args.end, table_id=args.table, data_source="bklog"))
first = base[0]["list"][0]
host = first["bk_host_id"]
term = ("bk_host_id", "eq", host)
cases = [
    ("ascending", {**raw, "order_by": ["dtEventTimeStamp", "__unique_key__"]}),
    ("projection", with_query(keep_columns=["dtEventTimeStamp", "log", "bk_host_id"])),
    ("exact-total", with_query(exact_total=True)),
    ("field-eq", with_query(conditions=condition(term))),
    ("field-and", with_query(conditions=condition(term, ("cloudId", "eq", first["cloudId"])))),
    ("field-or", with_query(conditions=condition(term, ("bk_host_id", "eq", -1), join="or"))),
    ("lucene-field", with_query(query_string=f"bk_host_id:{host}")),
    ("lucene-phrase", with_query(query_string='log:"' + next((w for w in first["log"].split() if w.isalpha()), "INFO") + '"')),

    ("empty", with_query(conditions=condition(("bk_host_id", "eq", -1)))),
]
if args.nested_field in first:
    cases.append(("nested-field", with_query(conditions=condition((args.nested_field, "eq", first[args.nested_field])))))
words = re.findall(r"[A-Za-z]{3,}", str(first.get(args.text_field, "")))
if words:
    cases.append(("sample-text-term", with_query(query_string=f'{args.text_field}:"{words[0]}"')))
for i, expression in enumerate(args.lucene):
    cases.append((f"lucene-extra-{i+1}", with_query(query_string=expression)))
for name, body in cases:
    run(name, "raw", body, empty=name == "empty")
wide = run("six-row-oracle", "raw", {**with_query(limit=6), "limit": 6})
page = run("search-after-1", "raw", {**raw, "is_search_after": True})
page_wide = run("search-after-six", "raw", {**with_query(limit=6), "limit": 6, "is_search_after": True})
if page and page_wide:
    expected = page_wide[0]["list"][3:6]
    run("search-after-2", "raw", {**raw, "is_search_after": True}, cursor=page, expected=expected)
    run("search-after-switch", "raw", {**raw, "is_search_after": True}, cursor=page, modes=(True, False), expected=expected)
if wide:
    expected = wide[0]["list"][3:6]
    run("offset", "raw", {**raw, "from": 3}, expected=expected)
scroll_body = {**raw, "scroll": "1m", "slice_max": 1}
scroll1 = run("scroll-1", "raw_with_scroll", scroll_body, scroll=True)
if scroll1:
    run("scroll-2", "raw_with_scroll", scroll_body, scroll=True,
        validate=lambda rows: not ({r["__unique_key__"] for r in rows} &
                                   {r["__unique_key__"] for r in scroll1[0]["list"]}))

for method in ("count", "sum", "avg", "max", "min"):
    q = {**query, "field_name": "gseIndex", "time_aggregation": {"function": method + "_over_time", "window": "1m"},
         "function": [{"method": "sum", "dimensions": ["cloudId"]}]}
    run("structured-" + method, "ts", {**raw, "query_list": [q], "metric_merge": "a", "step": "60s"})

# A single existing event bounds the label cardinality for unaggregated selectors.
m = args.metric + "{__unique_key__=" + json.dumps(str(first["__unique_key__"])) + "}"

exprs = {"selector": m, "matcher": m[:-1] + ',cloudId="' + str(first["cloudId"]) + '"}',
         "regex": m[:-1] + ',cloudId=~".+"}', "count-window": f"count_over_time({m}[1m])",
         "sum-window": f"sum_over_time({m}[1m])", "avg-window": f"avg_over_time({m}[1m])",
         "group": f"sum by (cloudId) (count_over_time({m}[1m]))",
         "topk": f"topk(2,sum by (cloudId) (count_over_time({m}[1m])))",
         "arithmetic": f"sum(count_over_time({m}[1m])) * 2",
         "instant": f"sum(count_over_time({m}[5m]))"}
for name in ("group", "topk", "arithmetic", "instant"):
    exprs[name] = exprs[name].replace(m, args.metric)
for name, expr in exprs.items():
    body = {**prom, "promql": expr, "instant": name == "instant"}
    run("prom-route-" + name, "check_promql", body, route=True)
    run("prom-" + name, "promql", body)

if args.second_table:
    second = dict(query, table_id=args.second_table, reference_name="b")
    for merge in (False, True):
        body = {**raw, "query_list": [query, second], "is_merge_db": merge}
        run(f"multi-route-{merge}", "check", body, route=True)
        run(f"multi-raw-{merge}", "raw", body)

for agg in (
    "quantile_over_time(0.95,M[5m])",
    "max_over_time(M[5m])",
    "min_over_time(M[5m])",
    "sum(count_over_time(M[1m])) / sum(count_over_time(M[5m]))",
    "sum(count_over_time(M[1m])) > 0",
):
    expr = agg.replace("M", args.metric)
    if agg.startswith(("quantile", "max_", "min_")):
        expr = f"max({expr})"
    body = {**prom, "promql": expr}
    run("extended-route-" + agg, "check_promql", body, route=True)
    run("extended-prom-" + agg, "promql", body)

sys.exit(int("FAIL" in outcomes or "ERROR" in outcomes))
