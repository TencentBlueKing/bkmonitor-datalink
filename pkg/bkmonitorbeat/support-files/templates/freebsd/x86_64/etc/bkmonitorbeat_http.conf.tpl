# 子配置信息
type: http
name: {{ config_name | default("http_task", true) }}
version: {{ config_version| default("1.1.1", true) }}

dataid: {{ data_id | default(1011, true) }}
# 缓冲区最大空间
max_buffer_size: {{ max_buffer_size | default(10240, true) }}
# 最大超时时间
max_timeout: {{ max_timeout | default("30s", true) }}
# 最小检测间隔
min_period: {{ min_period | default("3s", true) }}
# 任务列表
tasks: {% for task in tasks %}
  - task_id: {{ task.task_id }}
    bk_biz_id: {{ task.bk_biz_id }}
    period: {{ task.period }}
    target_ip_type: {{ task.target_ip_type | default(0, true) }}
    dns_check_mode: {{ task.dns_check_mode | default("single", true) }}
    available_duration: {{ task.available_duration }}
    insecure_skip_verify: {{ task.insecure_skip_verify | lower }}
    disable_keep_alives: {{ task.disable_keep_alives | lower }}
    # 检测超时（connect+read总共时间）
    timeout: {{ task.timeout | default("3s", true) }}
    # 采集步骤
    steps: {% for step in task.steps %}
      - method: {{ step.method }}
        # 当配置的url_list不为空时，使用url_list，忽略url
        {% if step.url_list -%}
        url_list: {% for url in step.url_list %}
        - {{ url }}{% endfor %}
        {% else -%}
        url: {{ step.url }}{% endif %}
        headers: {% for key,value in step.headers.items() %}
            {{ key }}: {{ value }}
        {% endfor %}
        available_duration: {{ step.available_duration }}
        request: "{{ (step.request or '') | replace('\r\n', '\\n') | replace('\r', '\\n') | replace('\n', '\\n') }}"
        # 请求格式（raw/hex）
        request_format: {{ step.request_format | default("raw", true) }}
        response: {{ step.response or '' }}
        # 内容匹配方式
        response_format: {{ step.response_format | default("eq", true) }}
        response_code: {{ step.response_code }}{% endfor %}{% endfor %}
