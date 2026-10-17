// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// roundTripCase: the parser rewrites user SQL (adds FROM / LIMIT, maps fields) but must keep the meaning of every
// expression. Each case is a select list, WHERE, GROUP BY or ORDER BY fragment written in canonical form; the
// rendered SQL, with whitespace normalised, must contain the fragment unchanged.
type roundTripCase struct {
	area string // select | where | group | order | having
	expr string
	want string // expected fragment when field mapping changes it; empty means expr itself
	// state: "" renders the fragment unchanged; "reject" fails with "unsupported Doris SQL" (not modelled, so not
	// rewritten); "silent:<root cause>" is a known silent rewrite, kept failing here until its fix lands, then the
	// fix flips the row. Root causes are listed in the parser issue record.
	state string
}

var roundTripCases = []roundTripCase{
	// unary
	{"select", "-a AS x", "", ""},
	{"select", "+a AS x", "", ""},
	{"select", "-(a + b) AS x", "", ""},
	{"select", "a * -1 AS x", "", ""},
	{"where", "-a > 1", "", ""},
	{"where", "a > -1", "", ""},
	// arithmetic and precedence
	{"select", "a + b * c AS x", "", ""},
	{"select", "(a + b) * c AS x", "", ""},
	{"select", "a * (b + c) AS x", "", ""},
	{"select", "(a - b) / (a + b) AS x", "", ""},
	{"select", "a - (b - c) AS x", "", ""},
	{"select", "a % 3 AS x", "", ""},
	{"select", "((a + b) * c - d) / 2 AS x", "", ""},
	{"where", "a * 2 > 2", "", ""},
	{"where", "2 < a * 2", "", ""},
	{"where", "(a + b) * 2 > c - 1", "", ""},
	{"where", "a + 1 = b", "", ""},
	// BETWEEN
	{"where", "a BETWEEN 1 AND 5", "", ""},
	{"where", "a NOT BETWEEN 1 AND 5", "", ""},
	{"where", "a * 2 BETWEEN 1 AND 5", "", ""},
	{"where", "a BETWEEN 1 AND 5 AND b = 1", "", ""},
	// IN
	{"where", "a IN ('x', 'y')", "", ""},
	{"where", "a NOT IN ('x', 'y')", "", ""},
	{"where", "a IN (1, 2) AND b NOT IN (3)", "", ""},
	// LIKE
	{"where", "a LIKE 'x%'", "", ""},
	{"where", "a NOT LIKE 'x%'", "", ""},
	// NULL
	{"where", "a IS NULL", "", ""},
	{"where", "a IS NOT NULL", "", ""},
	{"where", "a IS NULL OR b = 1", "", ""},
	// logic
	{"where", "(a = 1 OR b = 2) AND c = 3", "", ""},
	{"where", "a = 1 OR (b = 2 AND c = 3)", "", ""},
	{"where", "NOT a = 1", "", ""},
	{"where", "NOT (a = 1 OR b = 2)", "", ""},
	// CASE
	{"select", "CASE WHEN a > 1 THEN 'x' ELSE 'y' END AS x", "", ""},
	{"select", "CASE WHEN a > 1 THEN b * 2 ELSE b END AS x", "", ""},
	{"select", "SUM(CASE WHEN a = 1 THEN 1 ELSE 0 END) AS x", "", ""},
	{"where", "CASE WHEN a > 1 THEN 1 ELSE 0 END = 1", "", ""},
	{"select", "CASE WHEN NOT a = 1 THEN 1 ELSE 0 END AS x", "", "silent:D"},
	{"select", "CASE WHEN a IN (1, 2) THEN 1 ELSE 0 END AS x", "", "silent:D"},
	{"select", "CASE WHEN a IS NULL THEN 1 ELSE 0 END AS x", "", "silent:D"},
	{"select", "CASE WHEN a BETWEEN 1 AND 5 THEN 1 ELSE 0 END AS x", "", ""},
	{"select", "CASE WHEN a LIKE 'x%' THEN 1 ELSE 0 END AS x", "", ""},
	{"select", "CASE a WHEN 1 THEN 'x' ELSE 'y' END AS x", "", "silent:D"},
	// boolean expressions as values
	{"select", "a = 1 AS x", "", "silent:D"},
	{"select", "a > 1 AND b < 2 AS x", "", "silent:D"},
	{"select", "IF(a > 1, 1, 0) AS x", "", "silent:D"},
	{"select", "SUM(IF(a = 1, 1, 0)) AS x", "", "silent:D"},
	{"select", "COUNT_IF(a > 1) AS x", "", "silent:D"},
	// HAVING
	{"having", "COUNT(*) > 1", "", "silent:E"},
	{"having", "n > 1", "", "silent:E"},
	{"having", "NOT COUNT(*) > 1", "", "silent:E"},
	// CAST
	{"select", "CAST(a AS BIGINT) AS x", "", ""},
	{"select", "CAST(a / 1000 AS BIGINT) AS x", "", ""},
	{"select", "CAST((a + 1) / 2 AS INT) * 2 AS x", "", ""},
	{"where", "CAST(a AS INT) > 1", "", ""},
	// functions with nested arithmetic
	{"select", "FLOOR(a / 60000) * 60000 AS x", "", ""},
	{"select", "FLOOR((a + 1) / 2) AS x", "", ""},
	{"select", "ROUND(a * 100 / b, 2) AS x", "", ""},
	{"select", "SUM(a) / COUNT(*) AS x", "", ""},
	{"select", "SUM(a * b) AS x", "", ""},
	// DISTINCT(a) and DISTINCT a mean the same; the parser always writes the former.
	{"select", "COUNT(DISTINCT a) AS x", "COUNT(DISTINCT(a)) AS x", ""},
	{"select", "MAX(a) - MIN(a) AS x", "", ""},
	{"where", "LENGTH(a) > 3", "", ""},
	{"where", "ABS(a - b) > 1", "", ""},
	// aliases
	{"select", "a AS x, b * 2 AS y", "", ""},
	{"select", "a + 1 AS `x y`", "", ""},
	// GROUP BY / ORDER BY expressions
	{"group", "FLOOR(a / 60000)", "", ""},
	{"group", "a, b", "", ""},
	{"order", "a DESC", "", ""},
	{"order", "a * 2 DESC", "", ""},
	{"order", "COUNT(*) DESC", "", ""},
	// field mapping: m is a dimension mapped to __ext.m, wherever it appears
	{"select", "m AS x", "__ext.m AS x", ""},
	{"select", "(m + 1) * 2 AS x", "(__ext.m + 1) * 2 AS x", ""},
	{"where", "m = 'x'", "__ext.m = 'x'", ""},
	{"where", "'x' = m", "'x' = __ext.m", ""},
	{"where", "2 < m", "2 < __ext.m", ""},
	{"where", "a = m", "a = __ext.m", ""},
	{"where", "m IN ('x')", "__ext.m IN ('x')", ""},
	{"where", "m BETWEEN 1 AND 5", "__ext.m BETWEEN 1 AND 5", ""},
	{"where", "m IS NULL", "__ext.m IS NULL", ""},
	{"where", "-m > 1", "-__ext.m > 1", ""},
	{"group", "m", "__ext.m", ""},
	{"order", "m DESC", "__ext.m DESC", ""},
}

var roundTripSpaces = regexp.MustCompile(`\s+`)

func normaliseRoundTrip(s string) string {
	s = roundTripSpaces.ReplaceAllString(strings.TrimSpace(s), " ")
	s = strings.ReplaceAll(s, "( ", "(")
	s = strings.ReplaceAll(s, " )", ")")
	return s
}

func roundTripQuery(c roundTripCase) string {
	switch c.area {
	case "where":
		return "SELECT COUNT(*) AS n FROM t.doris WHERE " + c.expr
	case "group":
		return "SELECT COUNT(*) AS n FROM t.doris GROUP BY " + c.expr
	case "order":
		return "SELECT COUNT(*) AS n FROM t.doris ORDER BY " + c.expr
	case "having":
		return "SELECT a, COUNT(*) AS n FROM t.doris GROUP BY a HAVING " + c.expr
	default:
		return "SELECT " + c.expr + " FROM t.doris"
	}
}

func roundTripFragment(c roundTripCase) string {
	if c.want != "" {
		c.expr = c.want
	}
	switch c.area {
	case "where":
		return "WHERE " + c.expr
	case "group":
		return "GROUP BY " + c.expr
	case "order":
		return "ORDER BY " + c.expr
	case "having":
		return "HAVING " + c.expr
	default:
		return "SELECT " + c.expr + " FROM"
	}
}

// TestParseDorisSQLWithVisitorRoundTrip checks every case against its state. Set
// DORIS_PARSER_ROUNDTRIP_REPORT=1 to log all results instead (survey mode): PASS keeps the fragment, REJECT is a
// named error, SILENT loses or reorders user content (checkRewritePreservesInput), MISMATCH differs but loses
// nothing.
func TestParseDorisSQLWithVisitorRoundTrip(t *testing.T) {
	report := os.Getenv("DORIS_PARSER_ROUNDTRIP_REPORT") == "1"
	for _, c := range roundTripCases {
		q := roundTripQuery(c)
		sql, err := ParseDorisSQLWithVisitor(metadata.InitHashID(context.Background()), q, &Option{
			DimensionTransform: func(s string) (string, string) {
				s = strings.Trim(s, "`")
				if s == "m" {
					return "__ext.m", ""
				}
				return s, ""
			},
		})
		got := normaliseRoundTrip(sql)
		want := normaliseRoundTrip(roundTripFragment(c))
		kept := err == nil && strings.Contains(strings.ToUpper(got), strings.ToUpper(want))
		status := "PASS"
		switch {
		case kept:
		case err != nil:
			status = "REJECT"
		case checkRewritePreservesInput(q, sql) != nil:
			status = "SILENT"
		default:
			status = "MISMATCH"
		}
		if report {
			t.Logf("RT %s | %s | %s | got: %s | err: %v", status, c.area, c.expr, got, err)
			continue
		}
		switch {
		case c.state == "":
			if status != "PASS" {
				t.Errorf("%s %q: %s, rendered %q (err %v), want fragment %q", c.area, c.expr, status, got, err, want)
			}
		case c.state == "reject":
			if status != "REJECT" || !strings.Contains(err.Error(), "unsupported Doris SQL") {
				t.Errorf("%s %q: %s, want an unsupported Doris SQL error (got %q, err %v)", c.area, c.expr, status, got, err)
			}
		case strings.HasPrefix(c.state, "silent:"):
			if status != "SILENT" {
				t.Errorf("%s %q is marked %s but is now %s: update its state", c.area, c.expr, c.state, status)
			}
		default:
			t.Errorf("%s %q: unknown state %q", c.area, c.expr, c.state)
		}
	}
}
