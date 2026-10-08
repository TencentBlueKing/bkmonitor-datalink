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

### 设置 Feature Flag

服务优先读取 Redis，只有 Key 不存在时读取 Consul，校验成功后自动回填 Redis，无需手工迁移。回填采用 `SET NX`，已有或并发写入的 Redis 配置优先；回填失败会继续使用本次有效的 Consul 快照，并在后续调和时重试。回填后以 Redis 为准，后续 Consul 修改不再同步。Redis 读取失败或已有快照非法时保留运行中的最后有效配置，不重新读取 Consul；首次启动没有有效配置时持续重试。变更通过 Redis Pub/Sub 通知，通知丢失时由每分钟全量调和恢复。

使用运行服务相同的配置文件，将完整的 go-feature-flag JSON 快照写入 Redis：

```bash
unify-query --config /path/to/unify-query.yaml config set-feature-flags --file flags.json
```

命令复用 `redis` 的连接、认证、数据库和 `kv_base_path` 配置，支持单机和哨兵模式；先校验文件，再写入 `<redis.kv_base_path>:data:feature_flag`，并发布变更通知。写入替换整份配置，不设过期时间；`{}` 可清空开关配置。文件必须是 JSON，仓库中的 `featureFlag.yaml` 仅为格式示例。命令不会输出配置内容或凭据，校验、配置读取或写入失败时返回非零退出码。

### 查询与单项管理 Feature Flag

查询只读取 Redis，不触发 Consul 迁移；标准输出为 JSON，配置加载提示和日志写入标准错误，可直接导出整份快照或单个开关定义：

```bash
unify-query --config /path/to/unify-query.yaml config get-feature-flags > flags.json
unify-query --config /path/to/unify-query.yaml config get-feature-flags --name new-query > flag.json
```

`flags.json` 是供 `set-feature-flags` 使用的整份快照；单项新增、更新使用的 `flag.json` 只包含该开关的完整定义，不带外层开关名称，例如：

```json
{
  "variations": {"enabled": true, "disabled": false},
  "defaultRule": {"variation": "disabled"}
}
```

```bash
unify-query --config /path/to/unify-query.yaml config add-feature-flag --name new-query --file flag.json
unify-query --config /path/to/unify-query.yaml config update-feature-flag --name new-query --file flag.json
unify-query --config /path/to/unify-query.yaml config delete-feature-flag --name new-query
```

单项操作要求 Redis 已有快照；缺失时先等待在线 UQ 从 Consul 自动回填，或使用整份 `set-feature-flags` 明确初始化，避免单项新增抢占迁移并遗漏 Consul 中的其他开关。新增要求名称尚不存在；更新和删除要求名称已存在。更新替换该开关的完整定义，所有单项操作均保留其他开关，使用 SDK 校验更新后的完整快照，不设过期时间，并发布通知供在线 UQ 刷新。删除最后一个开关时写入 `{}`，保留 Redis Key，避免重新从 Consul 回填。Redis 快照或查询名称不存在时，查询命令返回错误。

单项写入通过 `WATCH` 事务检查并发修改；发生冲突时返回错误并提示重试，不自动覆盖其他写入。

### 重新从 Consul 迁移 Feature Flag

先准备好 Consul 中的配置，再在 UQ 容器内使用运行服务相同的配置文件执行，无需安装 `redis-cli`：

```bash
unify-query --config /path/to/unify-query.yaml config reset-feature-flags
```

命令仅删除 `<redis.kv_base_path>:data:feature_flag` 并发布刷新通知。在线 UQ 收到通知后重新读取 Consul，校验后通过 `SET NX` 回填 Redis，仍不设过期时间；通知丢失时由每分钟调和重试。命令成功表示 Redis 快照已删除，不表示回填已完成；没有在线实例时会在下次启动迁移。不要用 `set-feature-flags --file` 写入 `{}` 代替重置，已有空快照不会触发 Consul 回填。

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

更多接口详情请查看 Swagger 文档。
