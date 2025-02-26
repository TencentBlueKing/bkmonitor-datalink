// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sqlExpr

import (
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	Doris         = "doris"
	DorisTypeText = "text"
)

type DorisSQLExpr struct {
	DefaultSQLExpr

	fieldsMap map[string]string
}

var _ SQLExpr = (*DorisSQLExpr)(nil)

func (s *DorisSQLExpr) WithFieldsMap(fieldsMap map[string]string) SQLExpr {
	s.fieldsMap = fieldsMap

	s.DefaultSQLExpr.WithFieldsMap(fieldsMap)
	return s
}

func (s *DorisSQLExpr) ParserQueryString(qs string) (string, error) {
	expr, err := querystring.Parse(qs)
	if err != nil {
		return "", err
	}
	if expr == nil {
		return "", nil
	}

	return s.walk(expr)
}

func (s *DorisSQLExpr) ParserAllConditions(allConditions metadata.AllConditions) (string, error) {
	return s.DefaultSQLExpr.WithTransformDimension(s.dimTransform).ParserAllConditions(allConditions)
}

func (s *DorisSQLExpr) checkMatchALL(k string) bool {
	if s.fieldsMap != nil {
		if t, ok := s.fieldsMap[k]; ok {
			if t == DorisTypeText {
				return true
			}
		}
	}
	return false
}

func (s *DorisSQLExpr) dimTransform(field string) string {
	fs := strings.Split(field, ".")
	if len(fs) > 1 {
		return fmt.Sprintf("CAST(`%s`[\"%s\"] AS STRING)", fs[0], strings.Join(fs[1:], `"]["`))
	}
	return fmt.Sprintf("`%s`", field)
}

func (s *DorisSQLExpr) walk(e querystring.Expr) (string, error) {
	var (
		err   error
		left  string
		right string
	)

	if s == nil {
		return "", nil
	}

	switch c := e.(type) {
	case *querystring.NotExpr:
		left, err = s.walk(c.Expr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("NOT (%s)", left), nil
	case *querystring.OrExpr:
		left, err = s.walk(c.Left)
		if err != nil {
			return "", err
		}
		right, err = s.walk(c.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s OR %s)", left, right), nil
	case *querystring.AndExpr:
		left, err = s.walk(c.Left)
		if err != nil {
			return "", err
		}
		right, err = s.walk(c.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s AND %s", left, right), nil
	case *querystring.WildcardExpr:
		if c.Field == "" {
			err = fmt.Errorf(Doris + " " + ErrorMatchAll)
			return "", err
		}

		return fmt.Sprintf("%s LIKE '%%%s%%'", s.dimTransform(c.Field), c.Value), nil
	case *querystring.MatchExpr:
		if c.Field == "" {
			err = fmt.Errorf(Doris + " " + ErrorMatchAll + ": " + c.Value)
			return "", err
		}

		if s.checkMatchALL(c.Field) {
			return fmt.Sprintf("%s LIKE '%%%s%%'", s.dimTransform(c.Field), c.Value), nil
		}

		return fmt.Sprintf("%s = '%s'", s.dimTransform(c.Field), c.Value), nil
	case *querystring.NumberRangeExpr:
		if c.Field == "" {
			err = fmt.Errorf(Doris + " " + ErrorMatchAll)
			return "", err
		}

		var (
			start string
			end   string
		)
		if *c.Start != "*" {
			var op string
			if c.IncludeStart {
				op = ">="
			} else {
				op = ">"
			}
			start = fmt.Sprintf("%s %s %s", s.dimTransform(c.Field), op, *c.Start)
		}

		if *c.End != "*" {
			var op string
			if c.IncludeEnd {
				op = "<="
			} else {
				op = "<"
			}
			end = fmt.Sprintf("%s %s %s", s.dimTransform(c.Field), op, *c.End)
		}

		return fmt.Sprintf("%s", strings.Join([]string{start, end}, " AND ")), nil
	default:
		err = fmt.Errorf("expr type is not match %T", e)
	}

	return "", err
}

func init() {
	Register(Doris, &DorisSQLExpr{})
}
