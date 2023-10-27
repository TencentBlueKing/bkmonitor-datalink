### 命令参数

    Flags:
    
      -f, --field stringArray      "example bizid.alias_name:'float'"
      自定义field 格式为 字段.属性:属性值
      -i, --input string           input data
      输入数据文件路径 null 则为标准输入
      -l, --level string
      日志等级 可选warn or error 
      -n, --name string            data processor name
      清洗注册名称
          --num int                run time (default 1)
      -o, --output string          output data path
      输入 标准 or 文件
      -P, --payload string         payload format (default "json")
      输入类型 默认json
      -p, --pipeline stringArray
      pipeline 配置
      -r, --raw string
      清洗详细配置,一般为环境导出
      -t, --table stringArray
      rt 表配置
      -T, --timeout duration       timeout (default 5ns)
      超时时间
      -v, --verbose                show field list
      展示所输入配置详情
      注 不支持多表

### 输入示例
    -  输入命令
        ./transfer-dev test -f testm.tag:"metric" _bizid_.alias_name:'"bk_biz_id"' -f _bizid_.type:'float' -f _bizid_.tag:"metric" -f _bizid_.option.es_type:'"keyword"' --pipeline option.group_info_alias:"_private_"  --table option.es_unique_field_list:'["ip","path","gseIndex","_iteration_idx"]' --table schema_type:'"free"' -n flat.batch -T 10s
    - 标准输入
        {"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"ip":"127.0.0.1","testm":10086,"testD":"testD","timestamp":1554094763,"log":[1,2],"group_info": [{"tag": "aaa", "tag1": "aaa1"},{"tag": "bbb", "tag1": "bbb1"}]}
    - 标准输出
    {
      "data": [...],
      // 清洗数据
      "result": "[{\"dimensions\":{\"bk_cloud_id\":\"0\",\"bk_supplier_id\":\"0\",\"ip\":\"127.0.0.1\"},\"group_info\":[{\"tag\":\"aaa\",\"tag1\":\"aaa1\"},{\"tag\":\"bbb\",\"tag1\":\"bbb1\"}],\"metrics\":{\"_bizid_\":null,\"testm\":10086},\"time\":1554094763}]",
      // 清洗结果
      "name": "flat.batch",
      // 清洗名称
      "time": "",
      // 耗时    
      "count": 1,
      // 清洗出多少条 
      "error": null
      // error
    }
    
#### 其它示例
    // stdin + f + stdout
    ./transfer-dev test -f testm.tag:"metric" -f _bizid_.alias_name:'"bk_biz_id"' -f _bizid_.type:'float' -f _bizid_.tag:"metric" -f _bizid_.option.o_alias:"_private_"  --table option.es_unique_field_list:'["ip","path","gseIndex","_iteration_idx"]' --table schema_type:'"free"'  -n flat.batch -T 10s
    
    {"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"ip":"127.0.0.1","testm":10086,"testD":"testD","timestamp":1554094763,"log":[1,2],"group_info": [{"tag": "aaa", "tag1": "aaa1"},{"tag": "bbb", "tag1": "bbb1"}]}
    
    // file + f + stdout
    ./transfer-dev test -f testm.tag:"metric" -f _bizid_.alias_name:'"bk_biz_id"' -f _bizid_.type:'float' -f _bizid_.tag:"metric" -f _bizid_.option.o_alias:"_private_"  --table option.es_unique_field_list:'["ip","path","gseIndex","_iteration_idx"]' --table schema_type:'"free"' -i /home/ian/go/src/transfer/resources/collector/debug.dat -n flat.batch -T 10s
    
    // stdin + r + stdout
    ./transfer-dev test -T 100s -n regexp_log -r '{"result_table_list":[{"option":{"es_unique_field_list":["ip","path","gseIndex","_iteration_idx"]},"schema_type":"free","result_table":"2_log.durant_log1000008","field_list":[{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"log","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false,"es_index":true}},{"default_value":"","field_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"alias_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}},{"default_value":null,"field_name":"_bizid_","tag":"metric","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"alias_name":"bk_biz_id","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_cloudid_","tag":"metric","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"alias_name":"cloudId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_dstdataid_","tag":"metric","description":"\u76ee\u7684DataId","type":"int","is_config_by_user":true,"alias_name":"dstDataId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_errorcode_","tag":"metric","description":"\u9519\u8bef\u7801","type":"int","is_config_by_user":true,"alias_name":"errorCode","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_gseindex_","tag":"metric","description":"gse\u7d22\u5f15","type":"float","is_config_by_user":true,"alias_name":"gseIndex","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_path_","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"alias_name":"path","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_server_","tag":"dimension","description":"IP\u5730\u5740","type":"string","is_config_by_user":true,"alias_name":"serverIp","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_srcdataid_","tag":"metric","description":"\u6e90DataId","type":"int","is_config_by_user":true,"alias_name":"srcDataId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_time_","tag":"metric","description":"\u672c\u5730\u65f6\u95f4","type":"string","is_config_by_user":true,"alias_name":"logTime","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_utctime_","tag":"metric","description":"\u65f6\u95f4\u6233","type":"timestamp","is_config_by_user":true,"alias_name":"dtEventTimeStamp","unit":"","option":{"time_format":"datetime","es_format":"epoch_millis","es_type":"date","es_doc_values":false,"es_include_in_all":true,"time_zone":"0","es_index":true}},{"default_value":null,"field_name":"_worldid_","tag":"metric","description":"worldID","type":"string","is_config_by_user":true,"alias_name":"worldId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"value","tag":"metric","description":"","type":"float","is_config_by_user":true,"alias_name":"","unit":""},{"default_value":null,"field_name":"key","tag":"metric","description":"","type":"string","is_config_by_user":true,"alias_name":"","unit":""}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200145,"etl_config":"bk_log_regexp","option":{"group_info_alias":"_private_","encoding":"UTF-8","separator_regexp":"(?P<key>\\w+):\\s+(?P<value>\\w+)"}}'
    
    {"_bizid_":0,"_cloudid_":0,"_dstdataid_":1200124,"_errorcode_":0,"_gseindex_":1,"_path_":"/tmp/health_check.log","_private_":[{"bk_app_code":"bk_log_search"}],"_server_":"127.0.0.1","_srcdataid_":1200124,"_time_":"2019-10-08 17:41:49","_type_":0,"_utctime_":"2019-10-08 09:41:49","_value_":["option: 1"],"_worldid_":-1}
    
    ./transfer-dev test -T 100s -n regexp_log -r '{"result_table_list":[{"option":{"es_unique_field_list":["ip","path","gseIndex","_iteration_idx"]},"schema_type":"free","result_table":"2_log.durant_log1000008","field_list":[{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"log","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false,"es_index":true}},{"default_value":"","field_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"alias_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}},{"default_value":null,"field_name":"_bizid_","tag":"metric","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"alias_name":"bk_biz_id","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_cloudid_","tag":"metric","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"alias_name":"cloudId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_dstdataid_","tag":"metric","description":"\u76ee\u7684DataId","type":"int","is_config_by_user":true,"alias_name":"dstDataId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_errorcode_","tag":"metric","description":"\u9519\u8bef\u7801","type":"int","is_config_by_user":true,"alias_name":"errorCode","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_gseindex_","tag":"metric","description":"gse\u7d22\u5f15","type":"float","is_config_by_user":true,"alias_name":"gseIndex","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_path_","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"alias_name":"path","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_server_","tag":"dimension","description":"IP\u5730\u5740","type":"string","is_config_by_user":true,"alias_name":"serverIp","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_srcdataid_","tag":"metric","description":"\u6e90DataId","type":"int","is_config_by_user":true,"alias_name":"srcDataId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_time_","tag":"metric","description":"\u672c\u5730\u65f6\u95f4","type":"string","is_config_by_user":true,"alias_name":"logTime","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_utctime_","tag":"metric","description":"\u65f6\u95f4\u6233","type":"timestamp","is_config_by_user":true,"alias_name":"dtEventTimeStamp","unit":"","option":{"time_format":"datetime","es_format":"epoch_millis","es_type":"date","es_doc_values":false,"es_include_in_all":true,"time_zone":"0","es_index":true}},{"default_value":null,"field_name":"_worldid_","tag":"metric","description":"worldID","type":"string","is_config_by_user":true,"alias_name":"worldId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"value","tag":"metric","description":"","type":"float","is_config_by_user":true,"alias_name":"","unit":""},{"default_value":null,"field_name":"key","tag":"metric","description":"","type":"string","is_config_by_user":true,"alias_name":"","unit":""}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200145,"etl_config":"bk_log_regexp","option":{"group_info_alias":"_private_","encoding":"UTF-8","separator_regexp":"(?P<key>\\w+):\\s+(?P<value>\\w+)"}}' -i ../resources/collector/debug.dat
    
    
    // stdin + f + file
    ./transfer-dev test -f testm.tag:"metric" -f _bizid_.alias_name:'"bk_biz_id"' -f _bizid_.type:'float' -f _bizid_.tag:"metric" -f _bizid_.option.o_alias:"_private_"  --table option.es_unique_field_list:'["ip","path","gseIndex","_iteration_idx"]' --table schema_type:'"free"'  -n flat.batch -T 10s -o ../resources/collector/debugRes.dat
    
    {"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"ip":"127.0.0.1","testm":10086,"testD":"testD","timestamp":1554094763,"log":[1,2],"group_info": [{"tag": "aaa", "tag1": "aaa1"},{"tag": "bbb", "tag1": "bbb1"}]}
    
    // file + f + file
    ./transfer-dev test -f testm.tag:"metric" -f _bizid_.alias_name:'"bk_biz_id"' -f _bizid_.type:'float' -f _bizid_.tag:"metric" -f _bizid_.option.o_alias:"_private_"  --table option.es_unique_field_list:'["ip","path","gseIndex","_iteration_idx"]' --table schema_type:'"free"' -i /home/ian/go/src/transfer/resources/collector/debug.dat -n flat.batch -T 10s -o ../resources/collector/debugRes.dat
    
    
    
    
    json
    echo '{"_private_":[],"_value_":["{\"k1\":\"v1\"}"]}'|./transfer test -T 100s -n json_log -f k1.type:string -f k1.tag:"metric" -f k1.is_config_by_user:true -f log.tag:"metric" -f log.type:string -f log.is_config_by_user:true --pipeline option.group_info_alias:'_private_' --table option.es_unique_field_list:'[]'| python -m json.tool
    
    
    分隔
    
    echo '{"_private_":[],"_value_":["3,2,1"]}' | ./transfer test -T 100s -n separator_log -f bool.tag:"metric" -f bool.type:bool -f bool.is_config_by_user:true -f log.tag:"metric" -f log.type:string -f log.is_config_by_user:true --pipeline option.separator_field_list:'["int","string","bool"]' --pipeline option.group_info_alias:'_private_' --table option.es_unique_field_list:'[]' --pipeline option.separator:, -f int.tag:"metric" -f int.type:int -f int.is_config_by_user:true -f string.tag:"metric" -f string.type:string -f string.is_config_by_user:true -v | python -m json.tool
    
    bk_text
    
    echo '{"_private_":[],"_value_":["Tue Oct 22 22:48:00 CST 2019"]}' | ./transfer test -T 100s -n text_log  --pipeline option.group_info_alias:'_private_' --table option.es_unique_field_list:'[]' -f log.tag:"metric" -f log.type:string -f log.is_config_by_user:true --pipeline option.separator:, -v | python -m json.tool
    
    
    正则
    
    
    echo '{"_private_":[],"_value_":["option: 1"]}' | ./transfer test -T 100s -n regexp_log  --pipeline option.separator_regexp:'(?P<key>\w+):\s+(?P<value>\w+)' --pipeline option.group_info_alias:'_private_' --table option.es_unique_field_list:'["ip","path","gseIndex","_iteration_idx"]'  -f key.type:string -f key.tag:"metric" -f key.is_config_by_user:true -f value.type:string -f value.tag:"metric" -f value.is_config_by_user:true -f log.tag:"metric" -f log.type:string -f log.is_config_by_user:true | python -m json.tool
    

    
   