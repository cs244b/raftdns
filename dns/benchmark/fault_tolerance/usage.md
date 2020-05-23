1. Populate RRs:  
  `./benchmark -ip 127.0.0.1 -type populate -size 1000000`

2. Test pure read throughput, with public ip addresses of cluster node, testing type of throughput, current client thread number of 16, testing duration of 11 sec, reporting interval of 5 sec:   
  `./fault_tolerance --ip=18.144.9.6,54.241.141.210,54.67.86.37 --type=t --client=16 --duration=11 --interval=5`  
Result: on micro machine, this command gains 16.5k read throughput.