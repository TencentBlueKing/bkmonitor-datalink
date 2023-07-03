#! /bin/python
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# -*- coding:utf-8 -*-
import requests
import sys

basic_list=[]

host = "http://127.0.0.1:8500/v1/kv"
consul_prefix = "bk_bkmonitorv3_enterprise_production/metadata/data_id"
service_prefix = "bk_bkmonitorv3_enterprise_production/service"

# dataid : json_path
data_info = {
   1001:"config/1001_no_cmdb.json",
   1007:"config/1007_no_cmdb.json",
   1013:"config/1013_no_cmdb.json",
   1011:"config/1011_no_cmdb.json",
   1100004:"config/1100004.json",
   1500511:"config/1500511.json",
   1500870:"config/1500870.json",
   1500959:"config/1500959.json",
}

def get_data(path):
    f = open(path,"r")
    json_data = f.read()
    return json_data

# 调整consul的数据，提供给transfer做测试
def basic():
    test_list = basic_list

    # 以传参为优先
    if len(sys.argv) > 1:
        test_list = sys.argv[1:]

    # 删除所有现存的key，以重新部署
    split_symbol= "/"
    path = split_symbol.join((host,consul_prefix+"?recurse=true"))
    print("delete path:",path)
    result = requests.delete(path)
    print("result:",result)

    # 删除service下的内容
    path = split_symbol.join((host,service_prefix+"?recurse=true"))
    print("delete path:",path)
    result = requests.delete(path)
    print("result:",result)

    # 遍历测试用例列表，将json写入consul
    for test_item in test_list:
        path = split_symbol.join((host,consul_prefix,str(test_item)))
        config = get_data(data_info[int(test_item)])
        result = requests.put(path,config)
        print("add path:",path," result:",result)

# 正式执行
basic()