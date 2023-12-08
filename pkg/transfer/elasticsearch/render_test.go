// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// IndexRenderSuite
type IndexRenderSuite struct {
	testsuite.ETLSuite
}

// TestConfigTemplateRender
func (s *IndexRenderSuite) TestConfigTemplateRender() {
	conf := s.ShipperConfig.AsElasticSearchCluster()
	conf.SetIndex("test")

	date, err := time.Parse("2006-01-02", "2019-10-19")
	s.NoError(err)

	fn, err := elasticsearch.ConfigTemplateRender(conf)
	s.NoError(err)

	index, err := fn(elasticsearch.NewRecord(map[string]interface{}{
		"time": date,
	}))
	s.NoError(err)
	s.Equal("20191019_test", index)
}

// TestConfigTemplateRenderByConfig
func (s *IndexRenderSuite) TestConfigTemplateRenderByConfig() {
	conf := config.MetaClusterInfo{
		StorageConfig: map[string]interface{}{
			"base_index": "test",
		},
	}
	cluster := conf.AsElasticSearchCluster()

	now := time.Now()
	record := &elasticsearch.Record{
		Meta: map[string]interface{}{},
		Document: map[string]interface{}{
			"epoch_second":     now.Unix(),
			"epoch_nanosecond": now.UnixNano(),
			"rfc3339":          now.Format(time.RFC3339),
			"time":             now,
			"utc_time":         now.UTC(),
			"utc12_time":       now.In(utils.ParseFixedTimeZone(12)),
		},
	}

	cases := []struct {
		field    string
		timezone float64
		format   string
		time     time.Time
	}{
		{"epoch_second", 0, "2006010203", now.UTC()},
		{"epoch_nanosecond", 0, "2006010203", now.UTC()},
		{"rfc3339", 0, "2006010203", now.UTC()},
		{"time", 0, "2006010203", now.UTC()},
		{"utc_time", 0, "2006010203", now.UTC()},
		{"utc12_time", 0, "2006010203", now.UTC()},

		{"epoch_second", 0, "20060102", now.UTC()},
		{"epoch_nanosecond", 0, "20060102", now.UTC()},
		{"rfc3339", 0, "20060102", now.UTC()},
		{"time", 0, "20060102", now.UTC()},
		{"utc_time", 0, "20060102", now.UTC()},
		{"utc12_time", 0, "20060102", now.UTC()},

		{"epoch_second", 8, "2006010203", now.In(utils.ParseFixedTimeZone(8))},
		{"epoch_nanosecond", 8, "2006010203", now.In(utils.ParseFixedTimeZone(8))},
		{"rfc3339", 8, "2006010203", now.In(utils.ParseFixedTimeZone(8))},
		{"time", 8, "2006010203", now.In(utils.ParseFixedTimeZone(8))},
		{"utc_time", 8, "2006010203", now.In(utils.ParseFixedTimeZone(8))},
		{"utc12_time", 8, "2006010203", now.In(utils.ParseFixedTimeZone(8))},

		{"epoch_second", 12, "2006010203", now.In(utils.ParseFixedTimeZone(12))},
		{"epoch_nanosecond", 12, "2006010203", now.In(utils.ParseFixedTimeZone(12))},
		{"rfc3339", 12, "2006010203", now.In(utils.ParseFixedTimeZone(12))},
		{"time", 12, "2006010203", now.In(utils.ParseFixedTimeZone(12))},
		{"utc_time", 12, "2006010203", now.In(utils.ParseFixedTimeZone(12))},
		{"utc12_time", 12, "2006010203", now.In(utils.ParseFixedTimeZone(12))},

		{"epoch_second", -12, "2006010203", now.In(utils.ParseFixedTimeZone(-12))},
		{"epoch_nanosecond", -12, "2006010203", now.In(utils.ParseFixedTimeZone(-12))},
		{"rfc3339", -12, "2006010203", now.In(utils.ParseFixedTimeZone(-12))},
		{"time", -12, "2006010203", now.In(utils.ParseFixedTimeZone(-12))},
		{"utc_time", -12, "2006010203", now.In(utils.ParseFixedTimeZone(-12))},
		{"utc12_time", -12, "2006010203", now.In(utils.ParseFixedTimeZone(-12))},

		// postfix
		{"time", 0, "2006010203_x", now.UTC()},
	}

	for i, c := range cases {
		cluster.StorageConfig["index_datetime_timezone"] = c.timezone
		cluster.StorageConfig["index_datetime_format"] = c.format
		cluster.StorageConfig["index_datetime_field"] = c.field

		render, err := elasticsearch.ConfigTemplateRender(cluster)
		s.NoError(err, i)
		index, err := render(record)
		s.NoError(err, i)
		s.Equal(fmt.Sprintf("%s_test", c.time.Format(c.format)), index, i)
	}
}

// TestTimeBasedIndexAliasRender
func (s *IndexRenderSuite) TestTimeBasedIndexAliasRender() {
	conf := config.MetaClusterInfo{
		StorageConfig: map[string]interface{}{},
	}
	cluster := conf.AsElasticSearchCluster()

	now := time.Now()
	record := &elasticsearch.Record{
		Meta: map[string]interface{}{},
		Document: map[string]interface{}{
			"epoch_second":     now.Unix(),
			"epoch_nanosecond": now.UnixNano(),
			"rfc3339":          now.Format(time.RFC3339),
			"time":             now,
			"utc_time":         now.UTC(),
			"utc12_time":       now.In(utils.ParseFixedTimeZone(12)),
		},
	}

	cases := []struct {
		field    string
		timezone float64
		format   string
		time     time.Time
	}{
		{"epoch_second", 0, "prefix_2006010203_postfix", now.UTC()},
		{"epoch_nanosecond", 0, "prefix_2006010203_postfix", now.UTC()},
		{"rfc3339", 0, "prefix_2006010203_postfix", now.UTC()},
		{"time", 0, "prefix_2006010203_postfix", now.UTC()},
		{"utc_time", 0, "prefix_2006010203_postfix", now.UTC()},
		{"utc12_time", 0, "prefix_2006010203_postfix", now.UTC()},

		{"epoch_second", 0, "prefix_20060102_postfix", now.UTC()},
		{"epoch_nanosecond", 0, "prefix_20060102_postfix", now.UTC()},
		{"rfc3339", 0, "prefix_20060102_postfix", now.UTC()},
		{"time", 0, "prefix_20060102_postfix", now.UTC()},
		{"utc_time", 0, "prefix_20060102_postfix", now.UTC()},
		{"utc12_time", 0, "prefix_20060102_postfix", now.UTC()},

		{"epoch_second", 8, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(8))},
		{"epoch_nanosecond", 8, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(8))},
		{"rfc3339", 8, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(8))},
		{"time", 8, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(8))},
		{"utc_time", 8, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(8))},
		{"utc12_time", 8, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(8))},

		{"epoch_second", 12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(12))},
		{"epoch_nanosecond", 12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(12))},
		{"rfc3339", 12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(12))},
		{"time", 12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(12))},
		{"utc_time", 12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(12))},
		{"utc12_time", 12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(12))},

		{"epoch_second", -12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(-12))},
		{"epoch_nanosecond", -12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(-12))},
		{"rfc3339", -12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(-12))},
		{"time", -12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(-12))},
		{"utc_time", -12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(-12))},
		{"utc12_time", -12, "prefix_2006010203_postfix", now.In(utils.ParseFixedTimeZone(-12))},
	}

	for i, c := range cases {
		cluster.StorageConfig["index_datetime_timezone"] = c.timezone
		cluster.StorageConfig["index_datetime_field"] = c.field
		cluster.StorageConfig["index_alias_template"] = c.format

		render, err := elasticsearch.TimeBasedIndexAliasRender(cluster)
		s.NoError(err, i)
		index, err := render(record)
		s.NoError(err, i)
		s.Equal(c.time.Format(c.format), index, i)
	}
}

// TestIndexRender
func TestIndexRender(t *testing.T) {
	suite.Run(t, new(IndexRenderSuite))
}
