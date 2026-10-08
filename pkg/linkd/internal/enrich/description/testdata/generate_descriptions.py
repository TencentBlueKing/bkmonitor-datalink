"""使用指定 bk-monitor 仓库生成离线描述 fixture；不访问中间件。"""
import argparse
import json
import subprocess
from pathlib import Path
from bkmonitor_oracle import load_oracle

parser=argparse.ArgumentParser()
parser.add_argument('repository')
args=parser.parse_args()
oracle=load_oracle(args.repository)
commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=args.repository,text=True).strip()
cases=[]
for unit in ['', 'percent', 'percentunit', 'bytes', 's', 'custom-unit']:
    for value in [-1, 0, 0.5, 1, 10, 10.0, 1024, 1234567.8912345]:
        for method in ['gt','gte','lt','lte','eq','neq']:
            algorithm={'type':'Threshold','level':2,'unit_prefix':'','config':[[{'method':method,'threshold':'10'}]]}
            cases.append({'name':f'{unit or "empty"}/{value!r}/{method}','item_name':'AVG(metric)','unit':unit,'value':value,'connector':'and','algorithms':[algorithm],'expected':oracle(value,unit,'AVG(metric)',[algorithm])})
for unit,prefix,value,threshold in [('bytes','Ki',2048,'1'),('percentunit','%',0.9,'80'),('s','ms',1,'500')]:
    algorithm={'type':'Threshold','level':2,'unit_prefix':prefix,'config':[[{'method':'gte','threshold':threshold}]]}
    cases.append({'name':f'prefix/{unit}','item_name':'MAX(metric)','unit':unit,'value':value,'connector':'and','algorithms':[algorithm],'expected':oracle(value,unit,'MAX(metric)',[algorithm])})
for connector in ['and','or']:
    algorithms=[{'type':'Threshold','level':2,'config':[[{'method':'gt','threshold':'20'},{'method':'lt','threshold':'100'}],[{'method':'gte','threshold':'10'}]]},{'type':'Threshold','level':2,'config':[[{'method':'neq','threshold':10}]]}]
    for value in [9,10,21,101]:
        cases.append({'name':f'combined/{connector}/{value}','item_name':'AVG(metric)','unit':'','value':value,'connector':connector,'algorithms':algorithms,'expected':oracle(value,'','AVG(metric)',algorithms,connector)})
for value in [0,0.5,1,1.0,2]:
    algorithm={'type':'PingUnreachable','level':2,'config':[]}
    cases.append({'name':f'ping/{value!r}','item_name':'Ping','unit':'percentunit','value':value,'connector':'and','algorithms':[algorithm],'expected':oracle(value,'percentunit','Ping',[algorithm])})
target=Path(__file__).parent/'bkmonitor_descriptions.json'
target.write_text(json.dumps({'commit':commit,'cases':cases},ensure_ascii=False,indent=2)+'\n')
print('description oracle cases:',len(cases))
