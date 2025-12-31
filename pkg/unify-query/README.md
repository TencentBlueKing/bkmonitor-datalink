# Unify-Query

统一查询模块，提供可观测数据的统一查询入口，支持多种存储引擎的 PromQL 语法查询。

## 📚 文档

完整的项目文档请查看 [文档中心](./docs/README.md)，包含：

- [架构设计文档](./docs/architecture.md) - 系统架构和设计理念
- [核心模块文档](./docs/modules.md) - 各模块详细说明
- [存储引擎集成文档](./docs/storage-integration.md) - 如何集成新存储引擎
- [开发指南](./docs/development-guide.md) - 开发环境搭建和开发规范
- [故障排查指南](./docs/troubleshooting.md) - 常见问题排查方法
- [API 文档](./docs/api/relation.md) - API 接口说明
- [PromQL 文档](./docs/promql/promql.md) - PromQL 语法说明

## 🚀 快速开始

## 快速部署

在docker desktop上安装consul，redis，influxdb

### 本地创建redis数据

query/ts接口对应redis中三个hash，对应的键分别为
"bkmonitorv3:spaces:space_to_result_table"：这个hash用来存放space_id关联的所有result_table space id 是一个类似于租户的概念 根据 space id 来区别当前的租户可以看到哪些表, 并进行查询

"bkmonitorv3:spaces:result_table_detail"：这个hash用来存放result_table的详情 包括一些针对表的过滤详情

"bkmonitorv3:spaces:data_label_to_result_table"：这个hash用来存放result_table中的标签字段

例子：space_id=100147关联的result_table有一个叫做custom_report_aggate.base表，表的标签是custom

```bash
hset  bkmonitorv3:spaces:space_to_result_table  "a_100147"   "{\"2_bkapm_metric_asd12.__default__\":{\"filters\":[]},\"custom_report_aggate.base\":{\"filters\":[{\"bk_biz_id\":\"2\"}]},\"pushgateway_dbm_influxdb_bkpull.group1\":{\"filters\":[{\"bk_biz_id\":\"2\"}]}}"

hset "bkmonitorv3:spaces:result_table_detail" "custom_report_aggate.base"  "{\"storage_id\":8,\"storage_name\":\"\",\"cluster_name\":\"default\",\"db\":\"system\",\"measurement\":\"net\",\"vm_rt\":\"\",\"tags_key\":[],\"fields\":[\"speed_packets_recv\",\"speed_packets_sent\",\"speed_recv\",\"speed_sent\",\"speed_recv_bit\",\"speed_sent_bit\",\"bkmonitor_action_notice_api_call_count_total\",\"overruns\",\"carrier\",\"collisions\"],\"measurement_type\":\"bk_traditional_measurement\",\"bcs_cluster_id\":\"\",\"data_label\":\"custom\",\"bk_data_id\":1001}"

hset "bkmonitorv3:spaces:data_label_to_result_table"  "wz_test_613"   "[\"2_bkmonitor_time_series_1573001.__default__\",\"custom\"]"
```

此处按照下面的简单测试用例 （仅测试使用 非实际环境所包含字段和情况）向redis 写入hash信息
```bash
hset  bkmonitorv3:spaces:space_to_result_table  "mydb"   "{\"system.cpu_summary\":{\"filters\":[]},\"custom_report_aggate.base\":{\"filters\":[]}}"  // 假定在 mydb 对应的 space id 下有两张表为system.cpu_summary 和 custom_report_aggate.base

hset "bkmonitorv3:spaces:result_table_detail" "system.cpu_summary"  "{\"storage_id\":8,\"storage_name\":\"\",\"cluster_name\":\"default\",\"db\":\"mydb\",\"measurement\":\"system.cpu_summary\",\"vm_rt\":\"\",\"tags_key\":[],\"fields\":[\"_time\",\"usage\"],\"measurement_type\":\"bk_traditional_measurement\"}"  // 缓存system.cpu_summary的表字段信息

hset "bkmonitorv3:spaces:result_table_detail" "custom_report_aggate.base"  "{\"storage_id\":8,\"storage_name\":\"\",\"cluster_name\":\"default\",\"db\":\"mydb\",\"measurement\":\"custom_report_aggate.base\",\"vm_rt\":\"\",\"tags_key\":[],\"fields\":[\"_time\",\"bkmonitor_action_notice_api_call_count_total\"],\"measurement_type\":\"bk_traditional_measurement\"}"  // 缓存 custom_report_aggate.base 的表字段信息
```

### 本地创建influxdb数据

先在consul上创建influxdb实例，创建之后可以获取storageID为8的实例

```bash
consul kv put bkmonitorv3/unify-query/data/storage/8 {"address":"http://127.0.0.1:8086","username":"","password":"","type":"influxdb"}
```

在redis储存influxdb所在的集群信息和主机信息
```
hset bkmonitorv3:influxdb:cluster_info "default" "{\"host_list\":[\"influxdb\"],\"unreadable_host_list\":[\"default\"]}"
hset bkmonitorv3:influxdb:host_info "influxdb" "{\"domain_name\":\"127.0.0.1\",\"port\":8086,\"username\":\"\",\"password\":\"\",\"status\":false,\"backup_rate_limit\":0.0,\"grpc_port\":8089,\"protocol\":\"http\",\"read_rate_limit\":0.0}"
```

可以按照这几个请求和日志中的sql语句创建数据

test query: 假定我们在 system.cpu_summary 的表中 查找每 60s 的平均 CPU 负载

```bash
curl -X POST http://localhost:8086/write?db=mydb --data-binary 'system.cpu_summary usage=60.2 1716946204000000000'
curl -X POST http://localhost:8086/write?db=mydb --data-binary 'system.cpu_summary usage=60.2 1716946206000000000'   // 向influxdb 插入两段模拟数据

curl -X POST http://localhost:8086/write?db=mydb --data-binary 'system.cpu_summary usage=50.2 1716946904000000000'
curl -X POST http://localhost:8086/write?db=mydb --data-binary 'system.cpu_summary usage=70.2 1716946906000000000'
```


```
curl --location 'http://127.0.0.1:10205/query/ts' \
--header 'Content-Type: application/json' \
--data '{
    "space_uid": "influxdb",
    "query_list": [
        {
            "data_source": "",
            "table_id": "system.cpu_summary",
            "field_name": "usage",
            "field_list": null,
            "function": [
                {
                    "method": "mean",
                    "without": false,
                    "dimensions": [],
                    "position": 0,
                    "args_list": null,
                    "vargs_list": null
                }
            ],
            "time_aggregation": {
                "function": "avg_over_time",
                "window": "60s",
                "position": 0,
                "vargs_list": null
            },
            "reference_name": "a",
            "dimensions": [],
            "limit": 0,
            "timestamp": null,
            "start_or_end": 0,
            "vector_offset": 0,
            "offset": "",
            "offset_forward": false,
            "slimit": 0,
            "soffset": 0,
            "conditions": {
                "field_list": [],
                "condition_list": []
            },
            "keep_columns": [
                "_time",
                "a"
            ]
        }
    ],
    "metric_merge": "a",
    "result_columns": null,
    "start_time": "1716946204",
    "end_time": "1716946906",
    "step": "60s"
}'

{
    "series": [
        {
            "name": "_result0",
            "metric_name": "",
            "columns": [
                "_time",
                "_value"
            ],
            "types": [
                "float",
                "float"
            ],
            "group_keys": [],
            "group_values": [],
            "values": [
                [
                    1716946200000,  // 第一段 60s 的结果
                    60.2
                ],
                [
                    1716946860000, // 第二段 60s 的结果
                    60.2
                ]
            ]
        }
    ]
}
```

```
test lost sample in increase 假设我们在 custom_report_aggate.base 中查找条件为 notice_way 字段为 weixin 且 status 为 failed 在给定时间范围内以 5m 为窗口 每 60s 采集计算一次 bkmonitor_action_notice_api_call_count_total指标的增长情况
```

```bash
curl -X POST http://localhost:8086/write?db=mydb --data-binary 'custom_report_aggate.base,notice_way=weixin,status=failed bkmonitor_action_notice_api_call_count_total=10 1716946204000000000'
curl -X POST http://localhost:8086/write?db=mydb --data-binary 'custom_report_aggate.base,notice_way=weixin,status=failed bkmonitor_action_notice_api_call_count_total=15 1716946254000000000'
curl -X POST http://localhost:8086/write?db=mydb --data-binary 'custom_report_aggate.base,notice_way=weixin,status=failed bkmonitor_action_notice_api_call_count_total=15 1716946264000000000'
```

```
curl --location 'http://127.0.0.1:10205/query/ts' \
--header 'Content-Type: application/json' \
--data '{
    "space_uid": "a_100147",
    "query_list": [
        {
            "data_source": "bkmonitor",
            "table_id": "custom_report_aggate.base",
            "field_name": "bkmonitor_action_notice_api_call_count_total",
            "field_list": null,
            "function": null,
            "time_aggregation": {
                "function": "increase",
                "window": "5m0s",
                "position": 0,
                "vargs_list": null
            },
            "reference_name": "a",
            "dimensions": null,
            "limit": 0,
            "timestamp": null,
            "start_or_end": 0,
            "vector_offset": 0,
            "offset": "",
            "offset_forward": false,
            "slimit": 0,
            "soffset": 0,
            "conditions": {
                "field_list": [
                    {
                        "field_name": "notice_way",
                        "value": [
                            "weixin"
                        ],
                        "op": "eq"
                    },
                    {
                        "field_name": "status",
                        "value": [
                            "failed"
                        ],
                        "op": "eq"
                    }
                ],
                "condition_list": [
                    "and"
                ]
            },
            "keep_columns": null
        }
    ],
    "metric_merge": "a",
    "result_columns": null,
    "start_time": "1716946204",
    "end_time": "1716946264",
    "step": "60s"
}'

{
    "series": [
        {
            "name": "_result0",
            "metric_name": "",
            "columns": [
                "_time",
                "_value"
            ],
            "types": [
                "float",
                "float"
            ],
            "group_keys": [
                "notice_way",
                "status",
            ],
            "group_values": [
                "weixin",
                "failed",
            ],
            "values": [
                [
                    1716946200000,
                    6.8499
                ]
            ]
        }
    ]
}

```
test query support fuzzy `__name__`
```
curl --location 'http://127.0.0.1:10205/query/ts' \
--header 'Content-Type: application/json' \
--data '{
    "space_uid": "influxdb",
    "query_list": [
        {
            "data_source": "",
            "table_id": "system.cpu_summary",
            "field_name": ".*",    // 模糊正则查询 结果和第一个测试用例相同
			"is_regexp": true,
            "field_list": null,
            "function": [
                {
                    "method": "mean",
                    "without": false,
                    "dimensions": [],
                    "position": 0,
                    "args_list": null,
                    "vargs_list": null
                }
            ],
            "time_aggregation": {
                "function": "avg_over_time",
                "window": "60s",
                "position": 0,
                "vargs_list": null
            },
            "reference_name": "a",
            "dimensions": [],
            "limit": 0,
            "timestamp": null,
            "start_or_end": 0,
            "vector_offset": 0,
            "offset": "",
            "offset_forward": false,
            "slimit": 0,
            "soffset": 0,
            "conditions": {
                "field_list": [],
                "condition_list": []
            },
            "keep_columns": [
                "_time",
                "a"
            ]
        }
    ],
    "metric_merge": "a",
    "result_columns": null,
    "start_time": "1716946204",
    "end_time": "1716946906",
    "step": "60s"
}'

{
    "series": [
        {
            "name": "_result0",
            "metric_name": "",
            "columns": [
                "_time",
                "_value"
            ],
            "types": [
                "float",
                "float"
            ],
            "group_keys": [],
            "group_values": [],
            "values": [
                [
                    1716946200000,
                    60.2
                ],
                [
                    1716946860000,
                    60.2
                ]
            ]
        }
    ]
}
```
创建完数据，可以用工具图形化显示，工具链接：https://github.com/CymaticLabs/InfluxDBStudio

---

## 🔧 构建和运行

### 构建

```bash
# 构建生产版本
make build

# 构建调试版本
make debug
```

### 运行

```bash
# 使用默认配置运行
./bin/unify-query run

# 指定配置文件
./bin/unify-query run --config /path/to/config.yaml
```

更多开发相关的内容，请查看 [开发指南](./docs/development-guide.md)。

---

## 📝 贡献

欢迎贡献代码！在提交 PR 之前，请：

1. 阅读 [开发指南](./docs/development-guide.md) 了解开发规范
2. 确保代码通过测试：`make test`
3. 确保代码通过检查：`make lint`
4. 更新相关文档

---

## 🐛 问题反馈

如果遇到问题，请：

1. 查看 [故障排查指南](./docs/troubleshooting.md)
2. 查看 [文档中心](./docs/README.md) 获取更多信息
3. 提交 Issue 描述问题

---

## 📄 许可证

本项目采用 MIT 许可证，详情请查看 LICENSE 文件。

## 📖 API 文档

完整的 API 文档请查看：

- **Swagger 文档**：[swagger.yaml](./docs/swagger.yaml) 或 [swagger.json](./docs/swagger.json)
- **关系查询 API**：[关系查询文档](./docs/api/relation.md)
- **PromQL 使用**：[PromQL 文档](./docs/promql/promql.md)

主要 API 接口：

- `POST /query/ts` - 使用结构体查询监控数据
- `POST /query/promql` - 通过 PromQL 语句查询监控数据
- `POST /check/query/ts` - 使用结构体校验查询
- `POST /query/ts/info/field_keys` - 查询指标列表
- `POST /query/ts/info/tag_keys` - 查询维度列表
- `POST /query/ts/info/tag_values` - 查询维度值
- `POST /api/v1/relation/multi_resource` - 查询关系多源
