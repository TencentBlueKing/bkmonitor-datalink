type: report

# 单条上限(字节)
max_length: {{ max_length }}

# 吞吐上限(条/s)
max_throughput: {{ max_throughput }}

# 事件名称长度限制
max_len_of_event_name: {{ max_len_of_event_name }}

# 接收数据的 dataid 相关配置，列表结构
config_list:
{% for item in items %}
  - dataid: {{ item.dataid }}

    # 数据类型，字符串表示，event or time_series
    datatype: {{ item.datatype }}

    # 数据格式版本，字符串表示，v1、v2、...
    version: {{ item.version }}

    # 数据上报速率，最大值 1000/min
    rate: {{ item.max_rate | default(1000, true) }}

    accesstoken: {{ item.access_token }}

    # 允许的最大未来时间偏移，默认 1 小时，3600 秒（单位：秒）
    max_future_time_offset: {{ item.max_future_time_offset | default(3600, true) }}
{% endfor %}
