# 查询映射关系接口

## 资源映射查询

通过关键维度，查询对应目标资源的关联维度信息

- api: /api/v1beta1/relation/resource
- method: POST
- request:


## 资源映射查询 (批量)

通过关键维度，查询对应目标资源的关联维度信息（批量）

- api: /api/v1beta1/relation/mult_resource
- method: POST
- header: X-Bk-Scope-Space-Uid: bkcc__2
- request:

```json
{
  "timestamp": 1693217460,
  "query_list": [
    {
      "target_type": "system",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "namespace": "blueking",
        "pod": "bk-applog-bkapp-filebeat-stdout-gnknx",
        "label": "label-1"
      }
    },
    {
      "target_type": "system",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "node": "node-127-0-0-1",
        "label": "label-1"
      }
    },
    {
      "target_type": "system",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000"
      }
    }
  ]
}
```
- response:
```json
{
  "data": [
    {
      "code": 200,
      "source_type": "pod",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "namespace": "blueking",
        "pod": "bk-applog-bkapp-filebeat-stdout-gnknx"
      },
      "target_type": "system",
      "target_list": [
        {
          "bk_target_ip": "127.0.0.1"
        }
      ]
    },
    {
      "code": 200,
      "source_type": "node",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "node": "node-127-0-0-1"
      },
      "target_type": "system",
      "target_list": [
        {
          "bk_target_ip": "127.0.0.1"
        }
      ]
    },
    {
      "code": 404,
      "message": "404 not found"
    }
  ]
}
```
