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
import json
import time

host_address = "http://127.0.0.1:8500/v1/kv"
total_prefix = "bk_monitorv3_enterprise_development/metadata/influxdb_info"
host_prefix = "host_info"
cluster_prefix = "cluster_info"
route_prefix = "router"
tag_prefix = "tag_info"

host_map = {"host1" : {
    "username":"",
    "password":"",
    "domain_name": "127.0.0.1",
    "port": 8086
},"host2" : {
    "username":"",
    "password":"",
    "domain_name": "127.0.0.1",
    "port": 8087
},"host3" : {
    "username":"",
    "password":"",
    "domain_name": "127.0.0.1",
    "port": 8088
},"host4" : {
    "username":"",
    "password":"",
    "domain_name": "127.0.0.1",
    "port": 8089
},"host5" : {
    "username":"",
    "password":"",
    "domain_name": "127.0.0.1",
    "port": 8090
}}


cluster_map = {
    "cluster1":{
    "host_list": ["host1","host2","host3","host4","host5"],
    # "host_list": ["host1","host2"],
    "unreadable_host": [],
    }
}


route_map = {
    "db1/table1" : {
    "cluster": "cluster1",
    "partition_tag": ["mytag"]
    }
}

split_symbol= "/"

def add_into_consul(prefix,map):
    for key in map:
        # 拼接字符串，合成consul的key
        path = split_symbol.join((host_address,total_prefix,prefix,key))
        data = json.dumps(map[key])
        requests.put(path,data) 
def del_from_consul(prefix):
    path = split_symbol.join((host_address,total_prefix,prefix+"?recurse=true"))
    print("delete path:",path)
    result = requests.delete(path)
    print("result:",result)

def notify_all():
    path = split_symbol.join((host_address,total_prefix,"version/"))
    requests.put(path,str(time.time()))

def notify_tag():
    path = split_symbol.join((host_address,total_prefix,tag_prefix,"version/"))
    requests.put(path,str(time.time()))

def deploy():
    # 从根目录级联删除所有信息
    del_from_consul(host_prefix)
    del_from_consul(cluster_prefix)
    del_from_consul(route_prefix)

    # 将用于测试的信息重新写入
    add_into_consul(host_prefix,host_map)
    add_into_consul(cluster_prefix,cluster_map)
    add_into_consul(route_prefix,route_map)

    # 通知proxy刷新路由
    notify_all()


del_from_consul(tag_prefix)
deploy()
