# 子配置信息
type: ping
name: {{ config_name | default('icmp_task', true) }}
version: {{ config_version| default('1.1.1', true) }}


dataid: {{ data_id | default(1100003, true) }}
# 缓冲区最大空间
max_buffer_size: {{ max_buffer_size | default(10240, true) }}
# 最大超时时间
max_timeout: {{ max_timeout | default('100s', true) }}
# 最小检测间隔
min_period: {{ min_period | default('3s', true) }}
# 任务列表, ICMP仅有一个task
tasks: {% for task in tasks %}
  - task_id: {{ task.task_id }}
    bk_biz_id: {{ task.bk_biz_id }}
    target_ip_type: {{ task.target_ip_type | default(0, true) }}
    dns_check_mode: {{ task.dns_check_mode | default("single", true) }}
    period: {{ task.period }}
    # 检测超时（connect+read总共时间）
    timeout: {{ task.timeout | default('3s', true) }}
    max_rtt: {{ task.max_rtt }}
    total_num: {{ task.total_num }}
    ping_size: {{ task.size }}
   {% set instances = get_hosts_by_node(task.node_list) %}
    targets: {% for host in task.target_host_list %}
    - target: {{ host.target}}
      target_type: {{ host.target_type | default("ip", true)}}{% endfor %}
   {% for host in task.target_hosts or get_hosts_by_node(config_hosts) %}
    - target: {{ host.ip}}
      target_type: {{ host.target_type | default('ip', true)}}{% endfor %}
    {% if instances %}{% for instance in instances -%}
    {% for output_field in task.output_fields -%}
    {% if instance[output_field] -%}
    - target: {{ instance[output_field] }}
      target_type: ip
    {% endif %}{% endfor %}{% endfor %}{% endif %}{% endfor %}
{% if labels %}labels:
{% for label in labels %}{% for key, value in label.items() %}{{"-" if loop.first else " "}} {{key}}: "{{ value }}"
{% endfor %}{% endfor %}
{% endif %}
