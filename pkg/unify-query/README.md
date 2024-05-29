# Unify-Query

## 描述
统一查询模块，提供可观测数据的统一查询入口，支持多种存储引擎的 PromQL 语法查询

## 快速部署

安装preCI，pre-commit，gtm插件， 参考链接：https://iwiki.woa.com/p/4009081966

在docker desktop上安装consul，redis，influxdb

### 本地创建redis数据

query/ts接口对应redis中三个hash，对应的键分别为
"bkmonitorv3:spaces:space_to_result_table"：这个hash用来存放space_id关联的所有result_table

"bkmonitorv3:spaces:result_table_detail"：这个hash用来存放result_table的详情

"bkmonitorv3:spaces:data_label_to_result_table"：这个hash用来存放result_table中的标签字段

例子：space_id=100147关联的result_table有一个叫做custom_report_aggate.base表，表的标签是custom

```bash
hset  bkmonitorv3:spaces:space_to_result_table  "a_100147"   "{\"2_bkapm_metric_asd12.__default__\":{\"filters\":[]},\"custom_report_aggate.base\":{\"filters\":[{\"bk_biz_id\":\"2\"}]},\"pushgateway_dbm_influxdb_bkpull.group1\":{\"filters\":[{\"bk_biz_id\":\"2\"}]}}"

hset "bkmonitorv3:spaces:result_table_detail" "custom_report_aggate.base"  "{\"storage_id\":8,\"storage_name\":\"\",\"cluster_name\":\"default\",\"db\":\"system\",\"measurement\":\"net\",\"vm_rt\":\"\",\"tags_key\":[],\"fields\":[\"speed_packets_recv\",\"speed_packets_sent\",\"speed_recv\",\"speed_sent\",\"speed_recv_bit\",\"speed_sent_bit\",\"bkmonitor_action_notice_api_call_count_total\",\"overruns\",\"carrier\",\"collisions\"],\"measurement_type\":\"bk_traditional_measurement\",\"bcs_cluster_id\":\"\",\"data_label\":\"custom\",\"bk_data_id\":1001}"

hset "bkmonitorv3:spaces:data_label_to_result_table"  "wz_test_613"   "[\"2_bkmonitor_time_series_1573001.__default__\",\"custom\"]"
```

### 本地创建influxdb数据

先在consul上创建influxdb实例，创建之后可以获取storageID为6的实例

```bash
consul kv put bkmonitorv3/unify-query/data/storage/6 {"address":"http://bk-monitor-influxdb-proxy-http2:10203","username":"","password":"","type":"influxdb"}
```

本地需要加段代码，用来获取主机信息，加在influxdb_router.go文件232行defer span.End(&err)语句后面，这段代码不要提交

```
      r.clusterInfo = make(influxdb.ClusterInfo)
      r.clusterInfo["default"] = &influxdb.Cluster{
      HostList: []string{"localhost"},
      }
      r.hostInfo = make(influxdb.HostInfo)
      r.hostInfo["localhost"] = &influxdb.Host{
      DomainName: "localhost",
      Port:       8086,
      Protocol:   "http",
      }
      r.hostStatusInfo = make(influxdb.HostStatusInfo)
      r.hostStatusInfo["localhost"] = &influxdb.HostStatus{
      Read: true,
      }
```

在influxdb\instance.go文件中的query方法可以获取查询sql语句，可以根据sql语句创建influxdb原始数据，创建完数据，可以用工具图形化显示，工具链接：https://blog.csdn.net/u012593638/article/details/106541755/

创建完数据，可以用单测数据测试数据是否正确

## 接口详情
```yaml
swagger: '2.0'
basePath: /
info:
   version: '0.1'
   title: API Gateway Resources
   description: ''
schemes:
   - http
paths:
   /query/promql:
      post:
         operationId: query_promql
         description: 通过 PromQL 语句查询监控数据
         tags:
            - query
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: post
               path: /query/ts/promql
               matchSubpath: false
               timeout: 0
               upstreams: {}
               transformHeaders: {}
            authConfig:
               userVerifiedRequired: false
            disabledStages: []
            descriptionEn:
   /query/ts:
      post:
         operationId: query_ts
         description: 使用结构体查询监控数据
         tags:
            - query
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: post
               path: /query/ts
               matchSubpath: false
               timeout: 0
               upstreams: {}
               transformHeaders: {}
            authConfig:
               userVerifiedRequired: false
            disabledStages: []
            descriptionEn:
   /check/query/ts:
      post:
         operationId: check_query_ts
         description: 使用结构体校验查询
         tags:
            - check
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: post
               path: /check/query/ts
               matchSubpath: false
               timeout: 0
               upstreams: { }
               transformHeaders: { }
            authConfig:
               userVerifiedRequired: false
            disabledStages: [ ]
            descriptionEn:
   /query/ts/exemplar:
      post:
         operationId: query_ts_exemplar
         description: 通过结构体查询 exemplar 数据
         tags:
            - query
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: post
               path: /query/ts/exemplar
               matchSubpath: false
               timeout: 0
               upstreams: {}
               transformHeaders: {}
            authConfig:
               userVerifiedRequired: false
            disabledStages: []
            descriptionEn:
   /query/ts/info/field_keys:
      post:
         operationId: info_field_keys
         description: 查询指标列表
         tags:
            - info
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: post
               path: /query/ts/info/field_keys
               matchSubpath: false
               timeout: 0
               upstreams: {}
               transformHeaders: {}
            authConfig:
               userVerifiedRequired: false
            disabledStages: []
            descriptionEn:
   /query/ts/info/series:
      post:
         operationId: info_series
         description: 查询 series 内容
         tags:
            - info
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: post
               path: /query/ts/info/series
               matchSubpath: false
               timeout: 0
               upstreams: {}
               transformHeaders: {}
            authConfig:
               userVerifiedRequired: false
            disabledStages: []
            descriptionEn:
   /query/ts/info/tag_keys:
      post:
         operationId: info_tag_keys
         description: 查询维度列表
         tags:
            - info
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: post
               path: /query/ts/info/tag_keys
               matchSubpath: false
               timeout: 0
               upstreams: {}
               transformHeaders: {}
            authConfig:
               userVerifiedRequired: false
            disabledStages: []
            descriptionEn:
   /query/ts/info/tag_values:
      post:
         operationId: info_tag_values
         description: 查询维度值
         tags:
            - info
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: post
               path: /query/ts/info/tag_values
               matchSubpath: false
               timeout: 0
               upstreams: {}
               transformHeaders: {}
            authConfig:
               userVerifiedRequired: false
            disabledStages: []
            descriptionEn:
   /query/ts/label/{label_name}/values:
      get:
         operationId: info_label_values
         description: 根据维度查询维度值
         tags:
            - info
         responses:
            default:
               description: ''
         x-bk-apigateway-resource:
            isPublic: true
            allowApplyPermission: true
            matchSubpath: false
            backend:
               type: HTTP
               method: get
               path: /query/ts/label/{label_name}/values
               matchSubpath: false
               timeout: 0
               upstreams: {}
               transformHeaders: {}
            authConfig:
               userVerifiedRequired: false
            disabledStages: []
            descriptionEn:
   /query/ts/struct_to_promql:
     post:
       operationId: transform_struct_to_promql
       description: 查询结构体转换为promql语句
       tags:
         - info
       responses:
         default:
           description: ''
       x-bk-apigateway-resource:
         isPublic: true
         allowApplyPermission: true
         matchSubpath: false
         backend:
           type: HTTP
           method: post
           path: /query/ts/struct_to_promql
           matchSubpath: false
           timeout: 0
           upstreams: {}
           transformHeaders: {}
         authConfig:
           userVerifiedRequired: false
         disabledStages: []
         descriptionEn:
   /query/ts/promql_to_struct:
     post:
       operationId: transform_promql_to_struct
       description: promql语句转换为结构体
       tags:
         - info
       responses:
         default:
           description: ''
       x-bk-apigateway-resource:
         isPublic: true
         allowApplyPermission: true
         matchSubpath: false
         backend:
           type: HTTP
           method: post
           path: /query/ts/promql_to_struct
           matchSubpath: false
           timeout: 0
           upstreams: {}
           transformHeaders: {}
         authConfig:
           userVerifiedRequired: false
         disabledStages: []
         descriptionEn:
   /api/v1/relation/multi_resource_range:
     post:
       operationId: relation_multi_resource_query_range
       description: 查询关系多源
       tags:
         - info
       responses:
         default:
           description: ''
       x-bk-apigateway-resource:
         isPublic: true
         allowApplyPermission: true
         matchSubpath: false
         backend:
           type: HTTP
           method: post
           path: /api/v1/relation/multi_resource_range
           matchSubpath: false
           timeout: 0
           upstreams: {}
           transformHeaders: {}
         authConfig:
           userVerifiedRequired: false
         disabledStages: []
         descriptionEn:
   /api/v1/relation/multi_resource:
     post:
       operationId: relation_multi_resource_query
       description: 查询关系多源
       tags:
         - info
       responses:
         default:
           description: ''
       x-bk-apigateway-resource:
         isPublic: true
         allowApplyPermission: true
         matchSubpath: false
         backend:
           type: HTTP
           method: post
           path: /api/v1/relation/multi_resource
           matchSubpath: false
           timeout: 0
           upstreams: {}
           transformHeaders: {}
         authConfig:
           userVerifiedRequired: false
         disabledStages: []
         descriptionEn:
```


