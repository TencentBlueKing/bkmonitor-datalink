# 子配置信息
type: metricbeat
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
    module:
      module: prometheus
      metricsets: ["collector"]
      enabled: true
      hosts: ["{{ metric_url }}"]
      metrics_path: ''
      namespace: {{ config_name }}
      dataid: {{ dataid }}
      {% if diff_metrics %}diff_metrics:
      {% for metric in diff_metrics %}- {{ metric }}
      {% endfor %}
      {% endif %}{% if metric_relabel_configs %}
      metric_relabel_configs:
    {% for config in metric_relabel_configs %}
        - source_labels: [{{ config.source_labels | join("', '") }}]
          {% if config.regex %}regex: '{{ config.regex }}'{% endif %}
          action: {{ config.action }}
          {% if config.target_label %}target_label: '{{ config.target_label }}'{% endif %}
          {% if config.replacement %}replacement: '{{ config.replacement }}'{% endif %}
    {% endfor %}
    {% endif %}
    {% if labels %}labels:
    {% for label in labels %}{% for key, value in label.items() %}{{ "-" if loop.first else " "  }} {{ key }}: "{{ value }}"
    {% endfor %}{% endfor %}
    {% endif %}
