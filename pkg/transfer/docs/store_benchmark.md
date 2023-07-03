## 内存型缓存

### 红黑树

```
goos: darwin
goarch: amd64
pkg: transfer/storage
BenchmarkStoreSet100_BSTMap-4               	 1000000	      1751 ns/op
BenchmarkStoreSet1000_BSTMap-4              	  500000	      2302 ns/op
BenchmarkStoreSet10000_BSTMap-4             	  500000	      2966 ns/op
BenchmarkStoreUpdate100_BSTMap-4            	 1000000	      1655 ns/op
BenchmarkStoreUpdate1000_BSTMap-4           	 1000000	      2226 ns/op
BenchmarkStoreUpdate10000_BSTMap-4          	  500000	      3044 ns/op
BenchmarkStoreGet100_BSTMap-4               	 1000000	      2118 ns/op
BenchmarkStoreGet1000_BSTMap-4              	  500000	      2632 ns/op
BenchmarkStoreGet10000_BSTMap-4             	  500000	      3613 ns/op
BenchmarkStoreExistsMissing100_BSTMap-4     	 1000000	      1200 ns/op
BenchmarkStoreExistsMissing1000_BSTMap-4    	 1000000	      1270 ns/op
BenchmarkStoreExistsMissing10000_BSTMap-4   	 1000000	      1391 ns/op
BenchmarkStoreExists100_BSTMap-4            	 1000000	      1615 ns/op
BenchmarkStoreExists1000_BSTMap-4           	  500000	      2414 ns/op
BenchmarkStoreExists10000_BSTMap-4          	  500000	      3114 ns/op
BenchmarkStoreDelete100_BSTMap-4            	     100	      2568 ns/op
BenchmarkStoreDelete1000_BSTMap-4           	    1000	      2366 ns/op
BenchmarkStoreDelete10000_BSTMap-4          	   10000	      2838 ns/op
BenchmarkStoreScan100_BSTMap-4              	  100000	     16458 ns/op
BenchmarkStoreScan1000_BSTMap-4             	   10000	    158694 ns/op
BenchmarkStoreScan10000_BSTMap-4            	     500	   2702041 ns/op
BenchmarkStoreCommit100_BSTMap-4            	  200000	      9627 ns/op
BenchmarkStoreCommit1000_BSTMap-4           	   10000	    102012 ns/op
BenchmarkStoreCommit10000_BSTMap-4          	    1000	   1427422 ns/op
PASS
ok  	transfer/storage	405.442s
```

### 哈希表

```
goos: darwin
goarch: amd64
pkg: transfer/storage
BenchmarkStoreSet100_HashMap-4               	 1000000	      1441 ns/op
BenchmarkStoreSet1000_HashMap-4              	 1000000	      1252 ns/op
BenchmarkStoreSet10000_HashMap-4             	 1000000	      1417 ns/op
BenchmarkStoreUpdate100_HashMap-4            	 1000000	      1096 ns/op
BenchmarkStoreUpdate1000_HashMap-4           	 1000000	      1188 ns/op
BenchmarkStoreUpdate10000_HashMap-4          	 1000000	      1274 ns/op
BenchmarkStoreGet100_HashMap-4               	 1000000	      1641 ns/op
BenchmarkStoreGet1000_HashMap-4              	 1000000	      1670 ns/op
BenchmarkStoreGet10000_HashMap-4             	 1000000	      1815 ns/op
BenchmarkStoreExistsMissing100_HashMap-4     	 1000000	      1107 ns/op
BenchmarkStoreExistsMissing1000_HashMap-4    	 1000000	      1122 ns/op
BenchmarkStoreExistsMissing10000_HashMap-4   	 1000000	      1131 ns/op
BenchmarkStoreExists100_HashMap-4            	 1000000	      1120 ns/op
BenchmarkStoreExists1000_HashMap-4           	 1000000	      1145 ns/op
BenchmarkStoreExists10000_HashMap-4          	 1000000	      1252 ns/op
BenchmarkStoreDelete100_HashMap-4            	     100	      2573 ns/op
BenchmarkStoreDelete1000_HashMap-4           	    1000	     11278 ns/op
BenchmarkStoreDelete10000_HashMap-4          	   10000	    108800 ns/op
BenchmarkStoreScan100_HashMap-4              	  100000	     13626 ns/op
BenchmarkStoreScan1000_HashMap-4             	   10000	    125480 ns/op
BenchmarkStoreScan10000_HashMap-4            	    1000	   1432116 ns/op
BenchmarkStoreCommit100_HashMap-4            	  200000	      8308 ns/op
BenchmarkStoreCommit1000_HashMap-4           	   20000	     79384 ns/op
BenchmarkStoreCommit10000_HashMap-4          	    2000	    882703 ns/op
PASS
ok  	transfer/storage	520.527s
```

同步字典

```
goos: darwin
goarch: amd64
pkg: transfer/storage
BenchmarkStoreSet100_Map-4               	 1000000	      1480 ns/op
BenchmarkStoreSet1000_Map-4              	 1000000	      1588 ns/op
BenchmarkStoreSet10000_Map-4             	 1000000	      1733 ns/op
BenchmarkStoreUpdate100_Map-4            	 1000000	      1319 ns/op
BenchmarkStoreUpdate1000_Map-4           	 1000000	      1369 ns/op
BenchmarkStoreUpdate10000_Map-4          	 1000000	      1615 ns/op
BenchmarkStoreGet100_Map-4               	 1000000	      1123 ns/op
BenchmarkStoreGet1000_Map-4              	 1000000	      1161 ns/op
BenchmarkStoreGet10000_Map-4             	 1000000	      1366 ns/op
BenchmarkStoreExistsMissing100_Map-4     	 5000000	       369 ns/op
BenchmarkStoreExistsMissing1000_Map-4    	 5000000	       368 ns/op
BenchmarkStoreExistsMissing10000_Map-4   	 5000000	       359 ns/op
BenchmarkStoreExists100_Map-4            	 3000000	       443 ns/op
BenchmarkStoreExists1000_Map-4           	 3000000	       482 ns/op
BenchmarkStoreExists10000_Map-4          	 2000000	       583 ns/op
BenchmarkStoreDelete100_Map-4            	     100	       581 ns/op
BenchmarkStoreDelete1000_Map-4           	    1000	       569 ns/op
BenchmarkStoreDelete10000_Map-4          	   10000	       687 ns/op
BenchmarkStoreScan100_Map-4              	  200000	      8871 ns/op
BenchmarkStoreScan1000_Map-4             	   20000	     85137 ns/op
BenchmarkStoreScan10000_Map-4            	    1000	   1625283 ns/op
BenchmarkStoreCommit100_Map-4            	  500000	      3198 ns/op
BenchmarkStoreCommit1000_Map-4           	   50000	     29269 ns/op
BenchmarkStoreCommit10000_Map-4          	    5000	    355869 ns/op
PASS
ok  	transfer/storage	1255.469s
```





## 文件型缓存

### bbolt

```
goos: darwin
goarch: amd64
pkg: transfer/storage
BenchmarkStoreSet100_BBolt-4               	    5000	    213617 ns/op
BenchmarkStoreSet1000_BBolt-4              	   10000	    198987 ns/op
BenchmarkStoreSet10000_BBolt-4             	   10000	    223306 ns/op
BenchmarkStoreUpdate100_BBolt-4            	   10000	    208909 ns/op
BenchmarkStoreUpdate1000_BBolt-4           	    5000	    217202 ns/op
BenchmarkStoreUpdate10000_BBolt-4          	   10000	    245678 ns/op
BenchmarkStoreGet100_BBolt-4               	  200000	      6101 ns/op
BenchmarkStoreGet1000_BBolt-4              	  300000	      5983 ns/op
BenchmarkStoreGet10000_BBolt-4             	  200000	      6965 ns/op
BenchmarkStoreExistsMissing100_BBolt-4     	  500000	      3938 ns/op
BenchmarkStoreExistsMissing1000_BBolt-4    	  300000	      4438 ns/op
BenchmarkStoreExistsMissing10000_BBolt-4   	  300000	      4509 ns/op
BenchmarkStoreExists100_BBolt-4            	  200000	      6156 ns/op
BenchmarkStoreExists1000_BBolt-4           	  200000	      6474 ns/op
BenchmarkStoreExists10000_BBolt-4          	  200000	      6181 ns/op
BenchmarkStoreDelete100_BBolt-4            	     100	    180668 ns/op
BenchmarkStoreDelete1000_BBolt-4           	    1000	    189705 ns/op
BenchmarkStoreDelete10000_BBolt-4          	   10000	    247420 ns/op
BenchmarkStoreScan100_BBolt-4              	   20000	     70925 ns/op
BenchmarkStoreScan1000_BBolt-4             	    2000	    667206 ns/op
BenchmarkStoreScan10000_BBolt-4            	     200	   6648311 ns/op
BenchmarkStoreCommit100_BBolt-4            	     100	  14077131 ns/op
BenchmarkStoreCommit1000_BBolt-4           	     100	  14970182 ns/op
BenchmarkStoreCommit10000_BBolt-4          	     100	  22031997 ns/op
PASS
ok  	transfer/storage	186.907s
```

### badger

```
goos: darwin
goarch: amd64
pkg: transfer/storage
BenchmarkStoreSet100_Badger-4               	   10000	    114514 ns/op
BenchmarkStoreSet1000_Badger-4              	   10000	    111597 ns/op
BenchmarkStoreSet10000_Badger-4             	   10000	    112788 ns/op
BenchmarkStoreUpdate100_Badger-4            	   10000	    111052 ns/op
BenchmarkStoreUpdate1000_Badger-4           	   10000	    114201 ns/op
BenchmarkStoreUpdate10000_Badger-4          	   10000	    118456 ns/op
BenchmarkStoreGet100_Badger-4               	  200000	      6208 ns/op
BenchmarkStoreGet1000_Badger-4              	  200000	      5675 ns/op
BenchmarkStoreGet10000_Badger-4             	  200000	      5988 ns/op
BenchmarkStoreExistsMissing100_Badger-4     	  300000	      4792 ns/op
BenchmarkStoreExistsMissing1000_Badger-4    	  300000	      5097 ns/op
BenchmarkStoreExistsMissing10000_Badger-4   	  200000	      6086 ns/op
BenchmarkStoreExists100_Badger-4            	  200000	      6157 ns/op
BenchmarkStoreExists1000_Badger-4           	  200000	      5653 ns/op
BenchmarkStoreExists10000_Badger-4          	  200000	      6615 ns/op
BenchmarkStoreDelete100_Badger-4            	     100	    104020 ns/op
BenchmarkStoreDelete1000_Badger-4           	    1000	    117102 ns/op
BenchmarkStoreDelete10000_Badger-4          	   10000	    116281 ns/op
BenchmarkStoreScan100_Badger-4              	   10000	    179831 ns/op
BenchmarkStoreScan1000_Badger-4             	    1000	   1124467 ns/op
BenchmarkStoreScan10000_Badger-4            	     100	  11104469 ns/op
BenchmarkStoreCommit100_Badger-4            	  200000	      6835 ns/op
BenchmarkStoreCommit1000_Badger-4           	  200000	      6411 ns/op
BenchmarkStoreCommit10000_Badger-4          	  200000	      6970 ns/op
PASS
ok  	transfer/storage	187.400s
```

