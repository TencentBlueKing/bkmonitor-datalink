// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function

import (
	"time"

	"github.com/prometheus/prometheus/model/labels"
)

func MatcherToMetricName(matchers ...*labels.Matcher) string {
	for _, m := range matchers {
		if m.Name == labels.MetricName {
			if m.Type == labels.MatchEqual || m.Type == labels.MatchRegexp {
				return m.Value
			}
		}
	}

	return ""
}

func RangeDateWithUnit(unit string, start, end time.Time, step int) (dates []string) {
	var (
		addYear  int
		addMonth int
		addDay   int
		toDate   func(t time.Time) time.Time
		format   string
	)

	switch unit {
	case "year":
		addYear = step
		format = "2006"
		toDate = func(t time.Time) time.Time {
			return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
		}
	case "month":
		addMonth = step
		format = "200601"
		toDate = func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
		}
	default:
		addDay = step
		format = "20060102"
		toDate = func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		}
	}

	for d := toDate(start); !d.After(toDate(end)); d = d.AddDate(addYear, addMonth, addDay) {
		dates = append(dates, d.Format(format))
	}

	return dates
}
