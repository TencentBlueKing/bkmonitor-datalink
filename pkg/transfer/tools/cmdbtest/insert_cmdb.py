# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import bson
# -*- coding: utf-8 -*-
import pymongo
from dateutil import parser

# 脚本实现三个功能
# 1. insert_host_to_target()  插入主机到某个目标模块
# 2. batch_insert_host()  根据设置的开始biz，set，module，host，自动生成拓扑，插入主机
# 3. inster_instance()  插入实例，由于实例表结果在 cmdb v3.9.x之后有改变，此处应该不适配。


# 全局实例，client
user = "cmdb"
password = "qk8xUrKaUark"  # cat /data/install/.app.token | grep cmdb
url = "127.0.0.1"  # source /data/install/utils.fc echo $MONGODB_IP0
port = "27017"
db = "cmdb"

dbclient = pymongo.MongoClient(
    "mongodb://%s:%s@%s:%s/%s" % (user, password, url, port, db))
dber = dbclient[db]
col = dber["cc_HostBase"]
col2 = dber["cc_ModuleHostConfig"]
col3 = dber["cc_ObjectBase"]
col_query = dber["cc_idgenerator"]
col4 = dber["cc_ApplicationBase"]
col_set = dber["cc_SetBase"]
col_module = dber["cc_ModuleBase"]
aa = 20
bb = 0
cc = 0
dd = 0
total_host_count = 600000
# biz_host_count = 0
# set_host_count = 0
# host_count = 0
# y = col.find_one({"bk_host_innerip": "127.0.0.1"})
# y['_id'] = str(y['_id'])
# print(json.dumps(y))

# a, b, c, d = 20, 0, 0, 0


setjson = {
    "description": "",
    "metadata": {
        "label": {
            "bk_biz_id": "3"
        }
    },
    "bk_set_id": 11111111111111,
    "bk_parent_id": 3,
    "bk_service_status": "1",
    "last_time": "2021-03-31T16:37:42.214Z",
    "create_time": "2021-03-31T16:37:42.214Z",
    "bk_set_desc": "",
    "bk_set_env": "3",
    "default": 0,
    "bk_supplier_account": "0",
    "bk_biz_id": 3,
    "bk_capacity": None,
    "bk_set_name": "一阶段500台"
}

modulejson = {
    "bk_module_name": "500",
    "service_template_id": 0,
    "bk_module_type": "1",
    "default": 0,
    "operator": None,
    "create_time": "2019-09-20T03:32:48.551Z",
    "bk_module_id": 222222222222,
    "last_time": "2019-09-20T03:38:21.803Z",
    "bk_biz_id": 3,
    "bk_set_id": 11,
    "metadata": {
        "label": {
            "bk_biz_id": "3"
        }
    },
    "service_category_id": 2,
    "bk_parent_id": 11,
    "bk_supplier_account": "0",
    "bk_bak_operator": None
}

hostjson = {
    "bk_os_bit": "64-bit", "bk_cpu_mhz": 2595,
    "bk_outer_mac": "", "docker_client_version": "18.09.9",
    "bk_sla": None, "bk_province_name": None,
    "bk_mem": 15884, "bk_os_type": "1",
    "bk_state_name": None, "bk_bak_operator": ["admin"],
    "bk_sn": "", "bk_cloud_inst_id": "",
    "bk_cloud_vendor": None, "last_time": "2021-03-31T09:00:11.012Z",
    "bk_host_name": "VM-1-7-centos", "bk_cpu": 8,
    "bk_cloud_host_status": None, "bk_host_outerip": [],
    "bk_comment": "", "operator": ["admin"],
    "bk_service_term": None, "bk_state": None,
    "docker_server_version": "18.09.9",
    "import_from": "2", "bk_os_version": "7.8.2003",
    "bk_cpu_module": "AMD EPYC 7K62 48-Core Processor",
    "create_time": "2020-10-22T03:44:17.687Z",
    "bk_isp_name": None, "bk_os_name": "linux centos",
    "bk_disk": 245, "bk_mac": "52:54:00:55:a9:57",
    "bk_supplier_account": "0", "bk_asset_id": "",
    "bk_cloud_id": 0,
    "bk_host_id": 1,
    "bk_host_innerip": ["127.0.0.1"],
}

hostconfigjson = {
    "bk_biz_id": 8,
    "bk_host_id": 10000,
    "bk_module_id": 79,
    "bk_set_id": 18,
    "bk_supplier_account": "0"
}
dnsjson = {
    "category_layer_1": "1", "category_layer_2": "8",
    "ciowner_a": "", "created_date": None, "offline_time": None,
    "status": "1", "ttl": "600", "bk_obj_id": "dns",
    "dns_domain_name": "hdq.spdb.com", "last_modified_date": None,
    "bk_inst_id": 2, "last_time": "2021-03-31T16:37:42.214Z",
    "bk_supplier_account": "0", "category_layer_3": "17",
    "dns_ip": "127.0.0.1", "dns_network_region": "办公局域网",
    "last_change_id": "", "bk_inst_name": "hdq.spdb.com_办公局域网_127.0.0.1",
    "ciowner_b": "", "comment": "",
    "online_time": None, "create_time": "2021-03-31T16:37:42.214Z"
}
bizjson = {"default": 0,
           "bk_biz_tester": "", "language": "1",
           "create_time": "2021-03-31T16:37:42.214Z",
           "operator": "", "time_zone": "Asia/Shanghai",
           "life_cycle": "2", "bk_supplier_account": "0",
           "bk_supplier_id": 0, "bk_biz_productor": "",
           "bk_biz_developer": "", "bk_biz_name": "蓝鲸",
           "last_time": "2021-03-31T16:37:42.214Z",
           "bk_biz_id": 1000, "bk_biz_maintainer": "admin"
           }


def insert_host_to_target(start_host_id, target_module_id,
                          target_set_id, biz_id, target_host_count):
    """
    插入主机到目标模块,生成拓扑信息.  注意这里只是生成拓扑 没有插入主机
    params: start_host_id: int,开始的host_id
    params: target_module_id: int,开始的module_id
    params: target_set_id: int,开始的set_id
    params: target_host_count: int, host_count 的数量
    """
    hid = start_host_id
    hcj = hostconfigjson
    # 插入topo到表 cc_ModuleHostConfig ，这里主要是插批量的主机和集群模块等
    while hid < start_host_id+target_host_count:
        hcj["bk_host_id"] = hid
        hcj["bk_set_id"] = target_set_id
        hcj["bk_module_id"] = target_module_id
        hcj["bk_biz_id"] = biz_id
        if ("_id" in hcj):
            hcj.pop("_id")
        for key_dj in hcj:
            if type(hcj[key_dj]).__name__ == "int":
                hcj[key_dj] = bson.Int64(hcj[key_dj])
        col2.insert_one(hcj)
        hid += 1


def batch_insert_host(hostconfigjson, host_count):
    """
        插入主机和拓扑
    :params: hostconfigjson: json, 从cc_ModuleHostConfig 中取出来的格式
    :params: a,b,c,d: int, 开始ip的值,a.b.c.d: 127.0.0.1
    """
    global aa, bb, cc, dd
    a, b, c, d = aa, bb, cc, dd
    ii = col_query.find_one({"_id": "cc_HostBase"})["SequenceID"]+1
    i = ii
    # 这里ip从 a.b.c.d 开始，压测前要确认这个开始的ip，不能有重复
    # 生成ip，插入 cc_HostBase, cc_ModuleHostConfig 表中
    while i < ii+host_count:
        hj = hostjson
        hcj = hostconfigjson
        hj["create_time"] = parser.parse("2021-03-31T16:37:42.214Z")
        hj["bk_host_innerip"] = ["%s.%s.%s.%s" % (a, b, c, d)]
        hj["bk_host_id"] = i
        hcj["bk_host_id"] = i
        d += 1
        if(d > 254):
            d = 0
            c += 1
        if(c > 254):
            b += 1
            c = 0
        if(b > 254):
            a += 1
            b = 0
        if("_id" in hj):
            hj.pop("_id")
        if("_id" in hcj):
            hcj.pop("_id")
        for key in hj:
            if type(hj[key]).__name__ == "int":
                hj[key] = bson.Int64(hj[key])
        for key_c in hcj:
            if type(hcj[key_c]).__name__ == "int":
                hcj[key_c] = bson.Int64(hcj[key_c])
        print(hj)
        col.insert_one(hj)
        col2.insert_one(hcj)
        i += 1
    col_query.update_one({"_id":"cc_HostBase"},{ "$set": { "SequenceID": i } })
    aa, bb, cc, dd = a, b, c, d


def batch_insert_module(biz_id, set_id, module_count, host_count):
    """
    批量插入模块
    :params: biz_id： int， 目标业务
    :params: set： int， 目标集群
    :params: module_count： int， 模块
    """
    module_host_count = host_count/module_count
    mm = col_query.find_one({"_id": "cc_ModuleBase"})["SequenceID"]
    m = mm
    while m < mm+module_count:
        m += 1
        mj = modulejson
        mj["bk_biz_id"] = biz_id
        mj["bk_parent_id"] = set_id
        mj["metadata"]["label"]["bk_biz_id"] = biz_id
        mj["bk_module_id"] = bson.Int64(m)
        mj["bk_module_name"] = "%dmodule" % m
        mj["last_time"] = parser.parse("2019-09-26T12:05:09.253Z")
        mj["vreate_time"] = parser.parse("2019-09-26T12:05:09.253Z")
        if ("_id" in mj):
            mj.pop("_id")
        print(mj)
        col_module.insert_one(mj)
        col_query.update_one({"_id": "cc_ModuleBase"}, {
            "$set": {"SequenceID": m}})
        host_conf_json = {
            "bk_biz_id": biz_id,
            "bk_host_id": 0,
            "bk_module_id": m,
            "bk_set_id": set_id,
            "bk_supplier_account": "0"
        }
        batch_insert_host(host_conf_json, module_host_count)


def batch_insert_set(biz_id, set_count, module_count, host_count):
    """
    批量插入集群
    :params: set_count: int, 要插入的set count 值
    """
    # cc_idgenerator 中的 SequenceID 是为了保持inst_id的唯一。其值一般总是等于对应模块的最后一个inst_id
    ss = col_query.find_one({"_id": "cc_SetBase"})["SequenceID"]
    s = ss
    set_host_count = host_count/set_count
    set_ids = []
    # 向 cc_SetBase 表中插入set信息
    while s < ss+set_count:
        s += 1
        sj = setjson
        set_ids.append(s)
        sj["bk_biz_id"] = biz_id
        sj["bk_parent_id"] = biz_id
        sj["metadata"]["label"]["bk_biz_id"] = biz_id
        sj["bk_set_name"] = "set%d" % s
        sj["bk_set_id"] = bson.Int64(s)
        sj["last_time"] = parser.parse("2019-09-26T12:05:09.253Z")
        sj["vreate_time"] = parser.parse("2019-09-26T12:05:09.253Z")
        if ("_id" in sj):
            sj.pop("_id")
        print(sj)
        col_set.insert_one(sj)
        # cc_idgenerator 这个表中 将 SequenceID 的数量设置为与 set 的数量一样
        col_query.update_one({"_id": "cc_SetBase"}, {
                             "$set": {"SequenceID": s}})
        # 向 cc_ModuleBase 插入 module 信息
        # batch_insert_module(biz_id, s, module_count, set_host_count)

    return set_ids, set_host_count


def batch_insert_biz(biz_count, set_count, module_count, host_count):
    #col_query.update_one({"_id":"cc_ObjectBase"},{ "$set": { "SequenceID": j } })
    ll = col_query.find_one({"_id": "cc_ApplicationBase"})["SequenceID"]
    l = ll
    biz_host_count = host_count/biz_count
    bizs = []
    # 插入业务
    while l < ll+biz_count:
        bj = bizjson
        l += 1
        bizs.append(l)
        bj["bk_biz_id"] = bson.Int64(l)
        bj["bk_biz_name"] = "%dIAM" % l
        bj["create_time"] = parser.parse("2021-03-31T16:37:42.214Z")
        bj["last_time"] = parser.parse("2021-03-31T16:37:42.215Z")
        if ("_id" in bj):
            bj.pop("_id")
        print(bj)
        col4.insert_one(bj)
        col_query.update_one({"_id": "cc_ApplicationBase"}, {
                             "$set": {"SequenceID": l}})
        # batch_insert_set(l, set_count, module_count, biz_host_count)
    return bizs, biz_host_count


def batch_del_host(biz_list):
    """
    批量删除业务下拓扑
    :params: biz_list: 列表，包含要删除的所有列表
    """
    # col4.delete_many({"bk_biz_id": {
    #     "$in": biz_list,
    # }})


def batch_del_module(biz_list, set_list=[], module_list=[]):
    """
    批量删除模块
    :params: biz_list: 列表，包含要删除的所有列表
    """
    if len(biz_list) != 0:
        col_module.delete_many({"bk_biz_id": {
            "$in": biz_list,
        }})
    if len(set_list) != 0:
        col_module.delete_many({"bk_set_id": {
            "$in": set_list,
        }})
    if len(module_list) != 0:
        col_module.delete_many({"bk_biz_id": {
            "$in": biz_list,
        }})


def batch_del_set(biz_list, set_list=[]):
    """
    批量删除集群下拓扑
    :params: biz_list: 列表，包含要删除的所有列表
    """
    if len(biz_list) != 0:
        col_set.delete_many({"bk_biz_id": {
            "$in": biz_list,
        }})
    if len(set_list) != 0:
        col_set.delete_many({"bk_set_id": {
            "$in": set_list,
        }})


def batch_del_biz(biz_list):
    """
    批量删除业务下拓扑
    :params: biz_list: 列表，包含要删除的所有列表
    """
    if len(biz_list) == 0:
        return
    results = col4.delete_many({"bk_biz_id": {
        "$in": biz_list,
    }})
    batch_del_set(biz_list)
    batch_del_module(biz_list)


def batch_del_topo(biz_list):
    col2.delete_many({"bk_biz_id": {
        "$in": biz_list,
    }})


def recover(biz_list):
    """
    恢复环境：由于业务等的隔离性，将插入的业务或其他拓扑下的东西清空。但SequenceID可能由于有人使用，有变动，故不变动此ID
    拓扑一般数量不大，需要批量删除的是主机。故逻辑主要集中在删除主机。
    """
    batch_del_biz(biz_list)


# 插入20个业务，每个业务下插入1个集群，每个集群下插入1个模块
# 将60w个业务均分在这20个业务下
bizs, set_count = batch_insert_biz(20, 1, 1, 600000)
for biz in bizs:
    setids, module_count = batch_insert_set(biz, 1, 1, set_count)
    for set in setids:
        batch_insert_module(biz, set, 1, module_count)


# 恢复环境，这里删除只删除了，业务，集群，模块，和拓扑信息，未删除主机
start_biz_id = 18
# biz_list = [start_biz_id+i for i in range(0, 20)]
# recover(biz_list)
# batch_del_topo(biz_list)


# 主机删除，建议直接操作mongo
# use cmdb
# db.cc_HostBase.deleteMany({"$and": [{"bk_host_id": {"$gte": "开始的host_id"}}, {"bk_host_id": {"$lt": "开始的host_id+压测数"}}]})