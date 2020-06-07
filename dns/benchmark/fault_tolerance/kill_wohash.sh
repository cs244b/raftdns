#!/bin/bash
# script of fault tolerance benchmark test, without hash server
# Kill and restart two dns servers sequentially
# Throughput log in wo_hash.log

# 3 client threads, 152 second running time, 1 throughput output / s
stdbuf -oL ./fault_tolerance --ip=18.144.9.6,54.241.141.210,54.67.86.37 --type=t --client=3 --duration=152 --interval=1 --quiet > wo_hash.log & 
sleep 30
curl -s 18.144.9.6:8090/kill >> wo_hash.log
sleep 30
curl -s 54.241.141.210:8090/kill >> wo_hash.log
sleep 30
curl -s 18.144.9.6:8090/start -d "--id 1 --cluster http://172.31.1.59:12379,http://172.31.13.16:12379,http://172.31.12.30:12379 --port 9121" >> wo_hash.log
sleep 30
curl -s 54.241.141.210:8090/start -d "--id 2 --cluster http://172.31.1.59:12379,http://172.31.13.16:12379,http://172.31.12.30:12379 --port 9121" >> wo_hash.log
