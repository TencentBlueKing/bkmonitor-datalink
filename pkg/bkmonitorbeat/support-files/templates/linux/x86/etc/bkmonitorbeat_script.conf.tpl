# 子配置信息
type: script
name: {{ config_name }}
version: {{ config_version }}

# 最大超时时间
max_timeout: {{ max_timeout | default(100, true) }}s
# 最小检测间隔
min_period: {{ min_period | default(3, true) }}s

dataid: {{ dataid }}

tasks:
  - task_id: {{ task_id }}
    bk_biz_id: {{ bk_biz_id }}
    # 周期
    period: {{ period }}s
    # 超时
    timeout: {{ timeout | default(60, true) }}s
    user_env: {}
    dataid: {{ dataid }}
    command: {{ command }}
    {% if labels %}labels:
    {% for label in labels %}{% for key, value in label.items() %}{{ "-" if loop.first else " "  }} {{ key }}: "{{ value }}"
    {% endfor %}{% endfor %}
    {% endif %}{% if username %}username: {{ username }}{% endif %}
