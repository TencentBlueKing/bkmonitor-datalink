##### 功能描述
    - 根据条件查询主机
##### 参数结构
    - /api/v3/findmany/proc/service_instance/details
##### 使用参数
    - {
        "metadata": {
          "label": {
            "bk_biz_id": "3"
          }
        },        
        "with_name": true,
        "bk_module_id": 58,        
        "selectors": [{
          "key": "key1",
          "operator": "notin",
          "values": ["value1"]
        }]
      }
##### 参数说明
     -  |字段|类型|必填|默认值|说明|Description|
        |---|---|---|---|---|---|
        |with_name|bool|是|无|是否包含名称||
        |bk_module_id|integer|是|无|模块ID||
        |search_key|integer|是|无| 服务实例名称过滤字段，contains过滤||
        |selectors|integer|是|无|label过滤功能，operator可选值: `=`,`!=`,`exists`,`!`,`in`,`notin`||        
        |metadata|object|是|无|元数据|metadata|
        |lable|object|是|无|标签，支持"bk_biz_id"|metadata|
        ```
        type CCSearchServiceInstanceRequest struct {
            *CommonArgs
            MetaData map[string]map[string]map[string]interface{} `json:"metadata"`
            ServiceTemplateId  int   `json:"service_template_id"`
            ServiceInstanceIds []int `json:"service_instance_ids"`
        }
        ```

##### 使用输出参数
      -   | 字段|类型|说明|Description|
          |---|---|---|---|
          |id|integer|服务模板ID||
          |bk_host_id|integer|主机ID||
          |bk_host_innerip|string|主机IP||
          |bk_module_id|integer|模块ID||
          |metadata|object|元数据|metadata||
          
##### input 示例
       - {
           "metadata": {
             "label": {
               "bk_biz_id": "3"
             }
           },
           "with_name": true,
           "bk_module_id": 58,
         }
##### output 示例
         
        - 
        {
                   "result": true,
                   "data": {
                     "count": 1,
                     "info": [
                       {
                         "metadata": {
                           "label": {
                             "bk_biz_id": "3"
                           }
                         },
                         "id": 55,
                         "name": "10.0.0.1_a1",
                         "service_template_id": 50,
                         "bk_host_id": 3,
                         "bk_host_innerip": "10.0.0.1",
                         "bk_module_id": 58,
                         "bk_supplier_account": "0"
                       }
                     ]
                   }
                 }
   