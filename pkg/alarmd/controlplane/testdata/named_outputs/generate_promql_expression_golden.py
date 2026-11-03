"""Regenerate promql_expression_golden.json from the platform's own compiler.

usage: python3 -I generate_promql_expression_golden.py <promql_expression.py> > promql_expression_golden.json

The argument is bkmonitor/data_source/promql_expression.py from a bk-monitor
checkout at the deployed revision (file sha256 5f83cd0c4d6c);
an older checkout refuses an unused alias and gives a different file. Each case records the compiled PromQL, or that it is refused; the Go
compiler is held to the same answer for every case.
"""

import importlib.util
import json
import sys

spec = importlib.util.spec_from_file_location("promql_expression", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

A = 'sum by (job) (rate(requests_total{status="success"}[5m]))'
B = 'sum by (job) (rate(requests_total{status="error"}[5m]))'
C = "sum by (job) (queue_depth)"


def q(alias, promql, interval=60, source=("prometheus", "time_series")):
    return {"data_source_label": source[0], "data_type_label": source[1], "alias": alias, "promql": promql,
            "agg_interval": interval}


CASES = [
    ("sum", [q("a", A), q("b", B)], "a + b"),
    ("ratio with bool and vector matching", [q("a", A), q("b", B)], "a / on(job) group_left() b > bool 0.5"),
    ("unused alias", [q("a", A), q("b", B), q("c", C)], "a + b"),
    ("functions and aggregators", [q("a", A), q("b", B)], "sum by (job) (a) / clamp_min(b, 1)"),
    ("scientific number", [q("a", A), q("b", B)], "a * 1e3 + b * .5"),
    ("string literal kept", [q("a", A), q("b", B)], 'label_replace(a, "dst", "b", "src", "(.*)") + b'),
    ("uppercase alias", [q("A", A), q("B", B)], "A + B"),
    ("and or unless", [q("a", A), q("b", B), q("c", C)], "a and b or c unless a"),
    ("ignoring list", [q("a", A), q("b", B)], "a + ignoring(instance, pod) b"),
    ("comparison ops", [q("a", A), q("b", B)], "a >= b != 0"),
    ("whitespace kept", [q("a", A), q("b", B)], "  a\t+\n b  "),
    ("unicode space", [q("a", A), q("b", B)], "a　+ b"),
    ("information separators as space", [q("a", A), q("b", B)], "a\x1c+\x1fb"),
    ("exponent without digits refused", [q("a", A), q("b", B)], "a * 1e + b"),
    ("signed exponent without digits refused", [q("a", A), q("b", B)], "a * 1e+ + b"),
    ("one query", [q("a", A)], "a"),
    ("empty expression", [q("a", A), q("b", B)], "   "),
    ("other data source", [q("a", A), q("b", B, source=("bk_monitor", "time_series"))], "a + b"),
    ("invalid alias", [q("1a", A), q("b", B)], "b"),
    ("reserved alias", [q("on", A), q("b", B)], "b"),
    ("reserved alias case", [q("OFFSET", A), q("b", B)], "b"),
    ("duplicate alias", [q("a", A), q("a", B)], "a"),
    ("empty promql", [q("a", "  "), q("b", B)], "a + b"),
    ("zero interval", [q("a", A, 0), q("b", B, 0)], "a + b"),
    ("bool interval", [q("a", A, True), q("b", B, True)], "a + b"),
    ("string interval", [q("a", A, "60"), q("b", B, "60")], "a + b"),
    ("float interval", [q("a", A, 60.0), q("b", B, 60.0)], "a + b"),
    ("different intervals", [q("a", A, 60), q("b", B, 120)], "a + b"),
    ("range selector", [q("a", A), q("b", B)], "rate(a[5m])"),
    ("outer offset", [q("a", A), q("b", B)], "a offset 5m"),
    ("unknown identifier", [q("a", A), q("b", B)], "a + c"),
    ("alias as function", [q("a", A), q("b", B)], "a(b)"),
    ("modifier without list", [q("a", A), q("b", B)], "a + on b"),
    ("bad label list", [q("a", A), q("b", B)], "a + on(job + x) b"),
    ("incomplete modifier", [q("a", A), q("b", B)], "a + b by"),
    ("unsupported token", [q("a", A), q("b", B)], "a + b; drop"),
    ("by after identifier is aggregation", [q("a", A), q("b", B)], "max by (job) (a) - b"),
    ("group_left without list", [q("a", A), q("b", B)], "a * on(job) group_left b"),
]

out = []
for name, configs, expression in CASES:
    case = {"name": name, "query_configs": configs, "expression": expression}
    try:
        case["promql"] = module.compile_promql_expression(configs, expression)
    except ValueError:
        case["refused"] = True
    out.append(case)
json.dump(out, sys.stdout, ensure_ascii=False, indent=1)
sys.stdout.write("\n")
