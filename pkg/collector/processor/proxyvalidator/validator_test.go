// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxyvalidator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
)

func TestNoneValidator(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        },
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                "",
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "unsupported validator"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesMissingMetricsField(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        },
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "metrics missing"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesMetricsEmpty(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {},
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        },
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "metrics cannot be empty"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesDataType(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": {
        "metrics": {"load": "1.0"},
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        },
        "timestamp": 1673429359843
    }
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "timeseries data expected []any, got map[string]interface {}"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesNilMetrics(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {"load": null},
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        },
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "value expected float64 type, got <nil>"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesEmptyMetrics(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": []
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "timeseries data cannot be empty"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesIllegalMetricsItemType(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": ["metrics"]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "timeseries each item expected map[string]any type, got string"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesIllegalMetrics(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {"load": "1.0"},
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        },
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "value expected float64 type, got string"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesMissingIllegalDimension(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "dimension": {
            "$module": "db",
            "location": "guangdong"
        },
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "name '$module' required match regex [^[a-zA-Z_][a-zA-Z0-9_]*$]"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesDimensionType(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "dimension": "dim",
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "dimension expected map[string]any type, got string"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesDimensionValueType(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "dimension": {"number": {"obj": {"num": 1}}},
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "dimension 'number' value expected string type, got map[string]interface {}"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesDimensionConv(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "dimension": {"number": 1, "enabled": true},
        "timestamp": 1673429359843
    }]
}
`
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	assert.NoError(t, validator.Validate(&pb))
}

func TestTimeSeriesTimestampType(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        },
        "timestamp": "1673429359843"
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "timestamp expected float64 type, got string"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestTimeSeriesFutureTimestamp(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        },
        "timestamp": 4673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "reject future timestamp"
	assert.Contains(t, validator.Validate(&pb).Error(), msg)
}

func TestTimeSeriesTimestampEmpty(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        }
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	assert.NoError(t, validator.Validate(&pb))
	t.Logf("TestTimeSeriesTimestampEmpty: %+v", pb.Data)
}

func TestTimeSeriesDimensionEmpty(t *testing.T) {
	content := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "timestamp": 1673429359843
    }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeTimeSeries,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	assert.NoError(t, validator.Validate(&pb))
	t.Logf("TestTimeSeriesDimensionEmpty: %+v", pb.Data)
}

func TestEventMissingTargetField(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "target missing"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventTargetFieldEmpty(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": "",
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "target cannot be empty"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventMissingEventField(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": "target",
       "event_name": "bar",
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "event missing"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventMissingEventContent(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": "target",
       "event_name": "bar",
       "event": {},
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "event.content missing"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventMissingEventNameField(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": "target",
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "event_name missing"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventDataType(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": {
       "target": "target",
       "event_name": "bar",
       "event": "name",
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "event data expected []any, got map[string]interface {}"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventNilEvent(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": []
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "event data cannot be empty"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventEventType(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": "target",
       "event_name": "bar",
       "event": "",
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "event expected map[string]any type, got string"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventEventNameType(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": "target",
       "event_name": 123,
       "event": "",
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "eventName expected string type, got float64"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventEventTargetType(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": 10,
       "event_name": "name",
       "event": "",
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 1673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "target expected string type, got float64"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventTimestampType(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": "target",
       "event_name": "bar",
       "event": {"content": "foo"},
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": "1673429359843"
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "timestamp expected float64 type, got string"
	assert.Equal(t, msg, validator.Validate(&pb).Error())
}

func TestEventFutureTimestamp(t *testing.T) {
	content := `
{
   "data_id": 1100001,
   "access_token": "1100001_accesstoken",
   "data": [{
       "target": "target",
       "event_name": "bar",
       "event": {"content": "foo"},
       "dimension": {
           "module": "db",
           "location": "guangdong"
       },
       "timestamp": 4673429359843
   }]
}
`
	validator := NewValidator(Config{
		Type:                dataTypeEvent,
		Version:             "v2",
		MaxFutureTimeOffset: 3600,
	})
	var pb define.ProxyData
	assert.NoError(t, json.Unmarshal([]byte(content), &pb))
	msg := "reject future timestamp"
	assert.Contains(t, validator.Validate(&pb).Error(), msg)
}
