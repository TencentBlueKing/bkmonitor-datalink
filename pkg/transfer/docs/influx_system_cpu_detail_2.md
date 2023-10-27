```
> show tag keys from system_cpu_detail_2
name: system_cpu_detail_2
tagKey
------
company_id
device_name
hostname
ip
plat_id
```



```
> show field keys from system_cpu_detail_2
name: system_cpu_detail_2
fieldKey fieldType
-------- ---------
idle     float
iowait   float
stolen   float
system   float
usage    float
user     float
```



| time                 | company_id | device_name | hostname  | idle               | iowait                | ip        | plat_id | stolen | system               | usage             | user                |
| -------------------- | ---------- | ----------- | --------- | ------------------ | --------------------- | --------- | ------- | ------ | -------------------- | ----------------- | ------------------- |
| 2018-12-08T00:00:06Z | 0          | cpu0        | license-1 | 0.8565570187955976 | 0.0009754707935317754 | 127.0.0.1 | 0       | 0      | 0.026758526842591925 | 24.22818792030208 | 0.11476488072175028 |

