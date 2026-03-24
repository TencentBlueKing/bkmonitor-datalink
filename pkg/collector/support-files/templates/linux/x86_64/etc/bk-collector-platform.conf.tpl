type: "platform"
processor:
{% if apdex_config is defined %}
  - name: "{{ apdex_config.name }}"
    config:
      calculator:
        type: "{{ apdex_config.type }}"
      rules:
        {%- for rule_config in apdex_config.rules %}
        - kind: "{{ rule_config.kind }}"
          predicate_key: "{{ rule_config.predicate_key }}"
          metric_name: "{{ rule_config.metric_name }}"
          destination: "{{ rule_config.destination }}"
          apdex_t: {{ rule_config.apdex_t }} # ms
        {%- endfor %}
{%- endif %}

{% if license_config is defined %}
  - name: "{{ license_config.name }}"
    config:
      enabled: {{ license_config.enabled }}
      expire_time: {{ license_config.expire_time }}
      tolerable_expire: {{ license_config.tolerable_expire }}
      number_nodes: {{ license_config.number_nodes }}
      tolerable_num_ratio: {{ license_config.tolerable_num_ratio }}
{%- endif %}

{% if attribute_config is defined %}
  - name: "{{ attribute_config.name }}"
    config:
      as_int:
        keys:
          {%- for key in attribute_config.as_int%}
          - "{{ key }}"
          {%- endfor %}
      assemble:
        {%- for config in attribute_config.assemble %}
        - destination: "{{ config.destination }}"
          predicate_key: "{{ config.predicate_key }}"
          default_from: "{{ config.default_from }}"
          rules:
            {%- for rule in config.rules %}
            - kind: "{{ rule.kind }}"
              separator: "{{ rule.separator }}"
              placeholder: "{{ rule.placeholder }}"
              {%- if rule.get("first_upper") %}
              first_upper:
                {%- for key in rule.get("first_upper", []) %}
                - "{{ key }}"
                {%- endfor %}
              {%- endif %}
              keys:
                {%- for key in rule.get("keys", []) %}
                - "{{ key }}"
                {%- endfor %}
            {%- endfor %}
        {%- endfor %}
      cut:
        {%- for config in attribute_config.cut %}
        - predicate_key: "{{ config.predicate_key }}"
          max_length: {{ config.max_length }}
          match:
            {%- for value in config.get("match", []) %}
            - "{{ value }}"
            {%- endfor %}
          keys:
            {%- for key in config.get("keys", []) %}
            - "{{ key }}"
            {%- endfor%}
        {%- endfor %}
      drop:
        {%- for config in attribute_config.drop %}
        - predicate_key: "{{ config.predicate_key }}"
          match:
            {%- for value in config.get("match", []) %}
            - "{{ value }}"
            {%- endfor %}
          keys:
            {%- for key in config.get("keys", []) %}
            - "{{ key }}"
            {%- endfor%}
        {%- endfor %}
{%- endif %}

{% if sampler_config is defined %}
  - name: "{{ sampler_config.name }}"
    config:
      type: "{{ sampler_config.type }}"
      sampling_percentage: {{ sampler_config.sampling_percentage }}
{%- endif %}

{% if qps_config is defined %}
  - name: "{{ qps_config.name }}"
    config:
      type: "{{ qps_config.type }}"
      qps: {{ qps_config.qps }}
      burst: {{ qps_config.qps }}
{%- endif %}

{% if field_normalizer_config is defined %}
  - name: "{{ field_normalizer_config.name }}"
    config:
      fields:
        {%- for field in field_normalizer_config.get("fields", []) %}
        - kind: "{{ field.kind }}"
          predicate_key: "{{ field.predicate_key }}"
          rules:
            {%- for rule in field.rules %}
            - key: "{{ rule.key }}"
              op: "{{ rule.op }}"
              values:
                {%- for value in rule.get("values", []) %}
                - "{{ value }}"
                {%- endfor %}
            {%- endfor %}
        {%- endfor %}
{%- endif %}

{% if token_checker_config is defined %}
  - name: "{{ token_checker_config.name }}"
    config:
      type: "{{ token_checker_config.type }}"
      version: "{{ token_checker_config.version }}"
      resource_key: "{{ token_checker_config.resource_key }}"
      salt: "{{ token_checker_config.salt }}"
      decoded_key: "{{ token_checker_config.decoded_key }}"
      decoded_iv: "{{ token_checker_config.decoded_iv }}"
      must_empty_token: {{ token_checker_config.must_empty_token | default("true") }}
      fixed_token: "{{ token_checker_config.fixed_token }}"
      traces_dataid: {{ token_checker_config.trace_data_id | default(0) }}
      metrics_dataid: {{ token_checker_config.metric_data_id | default(0) }}
      logs_dataid: {{ token_checker_config.log_data_id | default(0) }}
      profiles_dataid: {{ token_checker_config.profile_data_id | default(0) }}
      bk_biz_id: {{ token_checker_config.bk_biz_id | default(0) }}
      bk_app_name: "{{ token_checker_config.bk_app_name }}"
{%- endif %}

{% if resource_filter_config is defined %}
  - name: "{{ resource_filter_config.name }}"
    config:
      assemble:
        {%- for as_config in  resource_filter_config.assemble %}
        - destination: "{{ as_config.destination }}"
          separator: "{{ as_config.separator }}"
          keys:
            {%- for key in as_config.get("keys", []) %}
            - "{{ key }}"
            {%- endfor %}
        {%- endfor %}
      drop:
        keys:
          {%- for drop_key in resource_filter_config.get("drop", {}).get("keys", []) %}
          - "{{ drop_key }}"
          {%- endfor %}
      default_value:
        {%- for default_value_config in  resource_filter_config.default_value %}
        - type: "{{ default_value_config.type }}"
          key: "{{ default_value_config.key }}"
          value: "{{ default_value_config.value }}"
        {%- endfor %}
{%- endif %}

{% if resource_fill_dimensions_config is defined %}
  - name: "{{ resource_fill_dimensions_config.name }}"
    config:
      from_record:
        {%- for from_record_config in  resource_fill_dimensions_config.from_record %}
        - source: "{{ from_record_config.source }}"
          destination: "{{ from_record_config.destination }}"
        {%- endfor %}
      from_cache:
        key: "{{ resource_fill_dimensions_config.from_cache.key }}"
        cache_name: "{{ resource_fill_dimensions_config.from_cache.cache_name }}"
{%- endif %}

{% if metric_configs is defined %}
  # bk_apm_count
  {% if metric_configs.metric_bk_apm_count_config is defined %}
  - name: "{{ metric_configs.metric_bk_apm_count_config.name }}"
    config:
      operations:
        {%- for operation in metric_configs.metric_bk_apm_count_config.operations %}
        - type: "{{ operation.type }}"
          metric_name: "{{ operation.metric_name }}"
          rules:
            {%- for rule_config in operation.rules %}
            - kind: "{{ rule_config.kind }}"
              predicate_key: "{{ rule_config.predicate_key }}"
              dimensions:
                {%- for dimension_key in rule_config.dimensions %}
                - "{{ dimension_key }}"
                {%- endfor %}
            {%- endfor %}
        {%- endfor %}
  {%- endif %}

  # bk_apm_total
  {% if metric_configs.metric_bk_apm_total_config is defined %}
  - name: "{{ metric_configs.metric_bk_apm_total_config.name }}"
    config:
      operations:
        {%- for operation in metric_configs.metric_bk_apm_total_config.operations %}
        - type: "{{ operation.type }}"
          metric_name: "{{ operation.metric_name }}"
          rules:
            {%- for rule_config in operation.rules %}
            - kind: "{{ rule_config.kind }}"
              predicate_key: "{{ rule_config.predicate_key }}"
              dimensions:
                {%- for dimension_key in rule_config.dimensions %}
                - "{{ dimension_key }}"
                {%- endfor %}
            {%- endfor %}
        {%- endfor %}
  {%- endif %}

  # bk_apm_duration
  {% if metric_configs.metric_bk_apm_duration_config is defined %}
  - name: "{{ metric_configs.metric_bk_apm_duration_config.name }}"
    config:
      operations:
        {%- for operation in metric_configs.metric_bk_apm_duration_config.operations %}
        - type: "{{ operation.type }}"
          metric_name: "{{ operation.metric_name }}"
          rules:
            {%- for rule_config in operation.rules %}
            - kind: "{{ rule_config.kind }}"
              predicate_key: "{{ rule_config.predicate_key }}"
              dimensions:
                {%- for dimension_key in rule_config.dimensions %}
                - "{{ dimension_key }}"
                {%- endfor %}
            {%- endfor %}
        {%- endfor %}
  {%- endif %}

  # bk_apm_duration_max
  {% if metric_configs.metric_bk_apm_duration_max_config is defined %}
  - name: "{{ metric_configs.metric_bk_apm_duration_max_config.name }}"
    config:
      operations:
        {%- for operation in metric_configs.metric_bk_apm_duration_max_config.operations %}
        - type: "{{ operation.type }}"
          metric_name: "{{ operation.metric_name }}"
          rules:
            {%- for rule_config in operation.rules %}
            - kind: "{{ rule_config.kind }}"
              predicate_key: "{{ rule_config.predicate_key }}"
              dimensions:
                {%- for dimension_key in rule_config.dimensions %}
                - "{{ dimension_key }}"
                {%- endfor %}
            {%- endfor %}
          {%- endfor %}
  {%- endif %}

  # bk_apm_duration_min
  {% if metric_configs.metric_bk_apm_duration_min_config is defined %}
  - name: "{{ metric_configs.metric_bk_apm_duration_min_config.name }}"
    config:
      operations:
        {%- for operation in metric_configs.metric_bk_apm_duration_min_config.operations %}
        - type: "{{ operation.type }}"
          metric_name: "{{ operation.metric_name }}"
          rules:
            {%- for rule_config in operation.rules %}
            - kind: "{{ rule_config.kind }}"
              predicate_key: "{{ rule_config.predicate_key }}"
              dimensions:
                {%- for dimension_key in rule_config.dimensions %}
                - "{{ dimension_key }}"
                {%- endfor %}
            {%- endfor %}
          {%- endfor %}
  {%- endif %}

  # bk_apm_duration_sum
  {% if metric_configs.metric_bk_apm_duration_sum_config is defined %}
  - name: "{{ metric_configs.metric_bk_apm_duration_sum_config.name }}"
    config:
      operations:
        {%- for operation in metric_configs.metric_bk_apm_duration_sum_config.operations %}
        - type: "{{ operation.type }}"
          metric_name: "{{ operation.metric_name }}"
          rules:
            {%- for rule_config in operation.rules %}
            - kind: "{{ rule_config.kind }}"
              predicate_key: "{{ rule_config.predicate_key }}"
              dimensions:
                {%- for dimension_key in rule_config.dimensions %}
                - "{{ dimension_key }}"
                {%- endfor %}
            {%- endfor %}
        {%- endfor %}
  {%- endif %}

   # bk_apm_duration_delta
  {% if metric_configs.metric_bk_apm_duration_delta_config is defined %}
  - name: "{{ metric_configs.metric_bk_apm_duration_delta_config.name }}"
    config:
      operations:
        {%- for operation in metric_configs.metric_bk_apm_duration_delta_config.operations %}
        - type: "{{ operation.type }}"
          metric_name: "{{ operation.metric_name }}"
          rules:
            {%- for rule_config in operation.rules %}
            - kind: "{{ rule_config.kind }}"
              predicate_key: "{{ rule_config.predicate_key }}"
              dimensions:
                {%- for dimension_key in rule_config.dimensions %}
                - "{{ dimension_key }}"
                {%- endfor %}
            {%- endfor %}
        {%- endfor %}
  {%- endif %}

   # bk_apm_duration_bucket
  {% if metric_configs.metric_bk_apm_duration_bucket_config is defined %}
  - name: "{{ metric_configs.metric_bk_apm_duration_bucket_config.name }}"
    config:
      operations:
        {%- for operation in metric_configs.metric_bk_apm_duration_bucket_config.operations %}
        - type: "{{ operation.type }}"
          metric_name: "{{ operation.metric_name }}"
          buckets: {{ operation.buckets }}
          rules:
            {%- for rule_config in operation.rules %}
            - kind: "{{ rule_config.kind }}"
              predicate_key: "{{ rule_config.predicate_key }}"
              dimensions:
                {%- for dimension_key in rule_config.dimensions %}
                - "{{ dimension_key }}"
                {%- endfor %}
            {%- endfor %}
        {%- endfor %}
  {%- endif %}
{%- endif %}
