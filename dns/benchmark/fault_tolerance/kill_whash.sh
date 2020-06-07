#!/bin/bash
# script of fault tolerance benchmark test, with hash server
# Kill and restart two dns servers sequentially
# Throughput log in w_hash.log

# 16 client threads, 152 second running time, 1 throughput output / s
stdbuf -oL ./fault_tolerance --useHash --hashIp=54.183.63.131 --type=t --client=16 --duration=152 --interval=1 --quiet > w_hash.log &
sleep 30
curl -s 18.144.9.6:8090/kill 
stdbuf -oL echo "kill first \n" >> w_hash.log
sleep 30
curl -s 54.241.141.210:8090/kill
stdbuf -oL echo "kill second \n" >> w_hash.log
sleep 30
curl -s 18.144.9.6:8090/start -d "--id 1 --cluster http://172.31.1.59:12379,http://172.31.13.16:12379,http://172.31.12.30:12379 --port 9121" 
stdbuf -oL echo "restart first \n" >> w_hash.log
sleep 30
curl -s 54.241.141.210:8090/start -d "--id 2 --cluster http://172.31.1.59:12379,http://172.31.13.16:12379,http://172.31.12.30:12379 --port 9121"
stdbuf -oL echo "restart second \n" >> w_hash.log