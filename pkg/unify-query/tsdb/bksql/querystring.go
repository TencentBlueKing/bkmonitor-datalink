// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring"
)

var (
	ErrorMatchAll = "doris 不支持全字段检索"
)

func QueryStringToSQL(q string) (string, error) {
	e, err := querystring.Parse(q)
	if err != nil {
		return "", err
	}

	p := sqlParser{
		e: e,
	}

	return parser(p)
}

type sqlParser struct {
	e   querystring.Expr
	not bool
}

func parser(p sqlParser) (string, error) {
	var (
		err   error
		left  querystring.Expr
		right querystring.Expr
	)

	switch c := p.e.(type) {
	case *querystring.NotExpr:
		p.not = !p.not
		p.e = c.Expr
		return parser(p)
	case *querystring.OrExpr:
		left, err = parser(sqlParser{not: p.not, e: c.Left})
		if err != nil {
			return "", err
		}
		right, err = parser(sqlParser{not: p.not, e: c.Right})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("( %s OR %s)", left, right), nil
	case *querystring.AndExpr:
		left, err = parser(sqlParser{not: p.not, e: c.Left})
		if err != nil {
			return "", err
		}
		right, err = parser(sqlParser{not: p.not, e: c.Right})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("( %s AND %s)", left, right), nil
	case *querystring.MatchExpr:
		if c.Field == "" {
			err = fmt.Errorf(ErrorMatchAll + ": " + c.Value)
			return "", err
		}

		if p.not {
			return fmt.Sprintf("%s NOT LIKE '%%%s%%'", c.Field, c.Value), nil
		} else {
			return fmt.Sprintf("%s LIKE '%%%s%%'", c.Field, c.Value), nil
		}
	case *querystring.NumberRangeExpr:
		if c.Field == "" {
			err = fmt.Errorf(ErrorMatchAll)
			return "", err
		}

		var (
			start string
			end   string
		)
		if *c.Start != "*" {
			var op string
			if !p.not {
				if c.IncludeStart {
					op = ">="
				} else {
					op = ">"
				}
			} else {
				if c.IncludeStart {
					op = "<"
				} else {
					op = "<="
				}
			}
			start = fmt.Sprintf("%s %s %s", c.Field, op, *c.Start)
		}

		if *c.End != "*" {
			var op string
			if !p.not {
				if c.IncludeEnd {
					op = "<="
				} else {
					op = "<"
				}
			} else {
				if c.IncludeEnd {
					op = ">"
				} else {
					op = ">="
				}
			}
			end = fmt.Sprintf("%s %s %s", c.Field, op, *c.End)
		}

		if p.not {
			return fmt.Sprintf("NOT ( %s )", strings.Join([]string{start, end}, " AND ")), nil
		} else {
			return fmt.Sprintf("( %s )", strings.Join([]string{start, end}, " AND ")), nil
		}
	default:
		err = fmt.Errorf("expr type is not match %T", p.e)
	}

	return "", err
}
