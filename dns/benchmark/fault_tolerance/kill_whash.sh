#!/bin/bash
# w hash server
# Kill and restart one dns cluster server, 
# see throughput drop to 2/3 and return back to orignal value
./fault_tolerance --useHash --hashIp=54.183.63.131 --type=t --client=32 --duration=60 --interval=2 --quiet &
sleep 20
curl 18.144.9.6:8090/kill
sleep 20
curl 18.144.9.6:8090/start -d "--id 1 --cluster http://172.31.1.59:12379,http://172.31.13.16:12379,http://172.31.12.30:12379 --port 9121"