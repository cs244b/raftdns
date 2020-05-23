Can use benchmark.go to populate RR records, measure latency and throughput. 
  * For all commands, *ip* specifies a string of comma-separated ip addresses to send requests to.

* To populate 1m RRs:

  `./benchmark -ip 127.0.0.1 -type populate -size 1000000`

* To measure latency:

  * *size* specifies the range of domains to query
  * default number of client threads is 1
  * *query* specifies the number of queries made by client to measure latency

  `./benchmark -ip 127.0.0.1 -type latency -query 1000 -size 1000000`

* To measure throughput

  * *duration* specifies the number of seconds to run
  * *readratio* is in [0,1]: 1 means all requests are reads
  * *client* specifies the number of client threads, each of which send as many query sequentially as possible

  `./benchmark -ip 127.0.0.1 -type throughput -size 1000000 -duration 60 -client 8 -readratio 1`

  
