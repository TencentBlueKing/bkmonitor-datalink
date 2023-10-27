type: "report_v2"
token: "{{ bk_data_token }}"
bk_biz_id: {{ bk_biz_id }}

default:
  processor:
{% if token_config is defined %}
    - name: "{{ token_config.name }}"
      config:
        type: "proxy"
        proxy_dataid: {{ token_config.proxy_dataid }}
        proxy_token: "{{ token_config.proxy_token }}"
{%- endif %}

{% if qps_config is defined %}
    # Qps: Qps限流
    - name: "{{ qps_config.name }}"
      config:
        type: "{{ qps_config.type }}"
        qps: {{ qps_config.qps }}
        burst: {{ qps_config.qps }}
{%- endif %}

{% if validator_config is defined %}
    - name: "{{ validator_config.name }}"
      config:
        type: "{{ validator_config.type }}"
        version: "{{ validator_config.version }}"
        max_future_time_offset: {{ validator_config.max_future_time_offset }}
{%- endif %}
