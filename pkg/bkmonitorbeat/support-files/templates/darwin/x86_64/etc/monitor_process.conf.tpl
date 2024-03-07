# 子配置信息
name: proccustom_task
version: 1.0.0
type: proccustom
period: {{ config.period }}
dataid: {{ config.dataid }}
task_id: {{ config.taskid }}
port_dataid: {{ config.port_dataid }}
{% if config.match_pattern %}match_pattern: {{ config.match_pattern }}{% endif %}
{% if config.process_name %}process_name:  {{ config.process_name }}{% endif %}
extract_pattern: ""
{% if config.extract_pattern %}extract_pattern: {{ config.extract_pattern }}{% endif %}
{% if config.exclude_pattern %}exclude_pattern: {{ config.exclude_pattern }}{% endif %}
{% if config.pid_path %}pid_path: {{ config.pid_path }}{% endif %}
proc_metric: []
{% if config.port_detect %}port_detect: true {% else %}port_detect: false{% endif %}
ports: []
listen_port_only: false
report_unexpected_port: false
disable_mapping: false
# 注入的labels
labels:{% for label in config.labels %}
    {% for key, value in label.items() %}{{ "-" if loop.first else " "  }} {{ key }}: "{{ value }}"
    {% endfor %}{% endfor %}
{% if config.tags|length >  0 %}
tags:{% for key, value in config.tags.items() %}
  {{ key }}: "{{ value }}"{% endfor %}
  {% else %}
tags: null
{% endif %}
