# 子配置信息
type: ping

# 上报数据ID
dataid: {{ dataid }}

# 上报周期
period: {{ period | default(60) }}s

# 单个周期内执行ping的次数
total_num: {{ total_num | default(3, true) }}

# 单次最多同时ping的IP数量，默认20一批
max_batch_size: {{ max_batch_size | default(20) }}

# 配置下发间隔
config_refresh_interval: {{ config_refresh_interval | default(10) }}m

# ping相关配置超时
ping.size: {{ ping_size | default(16) }}
ping.timeout: {{ ping_timeout | default(3) }}s

# server本机相关信息
server.ip: {{ server_ip }}
server.cloud_id: {{ server_cloud_id }}
server.bk_host_id: {{ server_host_id }}

# pingList
config_list:
{% for item in ip_to_items[server_host_id] %}
    - target_ip: {{ item.target_ip }}
      target_cloud_id: {{ item.target_cloud_id }}
      target_biz_id: {{ item.target_biz_id }}
{% endfor %}
