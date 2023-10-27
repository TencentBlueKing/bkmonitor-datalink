- 写在前面
  - Consul中，一共分为三个主要的配置信息：data_id列表，具体data_id配置信息，data_id发现字段配置
  - 元数据与ETL的交互流程如图所示：
        1. 元数据管理模块将data_id信息写入到data_id列表及data_id具体信息的Consul配置下
        2. ETL读取data_id列表的信息，发现当前全量的data_id
        3. 读取所有data_id的具体信息并开始进行数据清洗及入库
        4. 对于清洗中发现的字段信息，将写入到Consul中
        5. 元数据管理监听data_id字段配置信息，对发现新的字段回写到元数据管理数据库中


![图片描述](/tfl/captures/2019-02/tapd_10158081_base64_1550044053_76.png)

- 所有data_id列表
  - 路径: bkmonitor\_{PLATFORM}\_{ENVIRONMENT}/metadata/data\_id/
  - 用法：请遍历该节点下的所有子节点名字列表，即为生效中的data_id列表

- 各个data_id的具体信息
  - 路径: bkmonitor\_{PLATFORM}\_{ENVIRONMENT}/metadata/data\_id/{data_id}
  - 数据格式: JSON格式的对象内容
  - 样例数据: 

```
{
    # ETL清洗配置名称，用于非标准数据上报清洗使用
    "etl_config":"basereport",
    # DATA_ID的特殊配置，按照数据库配置动态生成返回
    # 该功能项暂缓实现
    "option": {
        "use_source_time": false
    },
    # 数据源输出的结果表配置列表
    "result_table_list":[
        {
            # 结果表字段配置方式，可以有以下的选择
            # dynamic: 动态字段(已有配置字段，但可以自动发现)
            # fixed: 固定字段
            # free: 无固定字段(无任何配置字段，完全依赖自动发现)
            "schema_type":"dynamic",
            # 实际入库配置信息
            "shipper_list":[
                {
                    # 集群配置
                    "cluster_config":{
                        "domain_name":"10.1.1.1",
                        "port":8086
                    },
                    # 存储具体配置
                    "storage_config":{
                        "real_table_name":"new_table_v10",
                        "database":"system"
                    },
                    # 集群类型
                    "cluster_type":"influxdb"
                }
            ],
            # 对SaaS暴露的结果表名
            "result_table":"system.cpu",
            # 字段列表
            "field_list":[
                {
                    # 字段类型可有以下选项：
                    # int: 整形
                    # float: 浮点型
                    # string: 字符串
                    # timestamp: 时间戳
                    "type":"int",
                    # 是否由用户配置字段，可能存在字段已自动发现，但未由用户确认
                    "is_config_by_user":true,
                    # 字段标签，可以有以下的选项
                    # dimension: 维度字段
                    # metric: 指标字段
                    # timestamp: 时间戳
                    "tag":"dimension",
                    # 字段名
                    "field_name":"bk_biz_id"
                }
            ]
        }
    ],
   # 消息队列配置信息，可以参考入库配置信息
    "mq_config":{
        "cluster_config":{
            "domain_name":"kafka.service.consul",
            "port":9092
        },
        "storage_config":{
            "topic":"bkmonitor_130",
            "partition":2,
            "verify": {
                "username":"admin",
                "password":"admin"
            } 
        },
        "cluster_type":"kafka"
    },
    # 该数据源唯一ID标识
    "data_id":13
}
```
- 字段上报信息
  - 路径: bkmonitor\_{PLATFORM}\_{ENVIRONMENT}/metadata/data\_id/{data_id}/fields
  - 数据格式: JSON格式的数组，元素是对象内容，描述了字段内容
  - 样例数据:  （数据字段含义可以参考上面字段信息样例）

```
[{
    # 指标字段名称
    "metric":  {
        # 字段类型可有以下选项：
        # int: 整形
        # float: 浮点型
        # string: 字符串
        # timestamp: 时间戳
        "type":"float",
        # 字段名
        "field_name":"usage",
	"updated_time": "2018-09-09 10:10:10"
    },
    # 组成该条记录的维度字段列表
    "dimension": [{
        # 字段类型可有以下选项：
        # int: 整形
        # float: 浮点型
        # string: 字符串
        # timestamp: 时间戳
        "type":"string",
        # 字段名
        "field_name":"hostname",
	"updated_time": "2018-09-09 10:10:10"
    }],
    "result_table":  "table_name"
}]
```
