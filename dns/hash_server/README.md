# Consistent Hashing Server

This is a single-node consistent hashing server implementation. Ideally it should also be converted into a Raft cluster.

### How to run

```bash
# The config file tells the server how many clusters are there, and how many machines in each cluster.
# It uses these info to decide how to hash certain requests to certain servers
./hash_server --config hash_config.json

# To test this, open up multiple Docker instance under a common docker network, each with a different
# IP address and visible to each other.

docker network create --subnet=172.18.0.0/16 raftnet
docker build --tag raftdns:0.1 .
# Once inside of bash, you can do stuff easily
docker run --net raftnet -it --ip 172.18.0.5 raftdns:0.1 /bin/bash # Run dig here
docker run --net raftnet -it --ip 172.18.0.6 raftdns:0.1 /bin/bash # Run our consistent hashing server here
docker run --net raftnet -it --ip 172.18.0.10 raftdns:0.1 /bin/bash # Single-node DNS cluster
docker run --net raftnet -it --ip 172.18.0.11 raftdns:0.1 /bin/bash # Single-node DNS cluster 2
# On instance of 172.18.0.6
./hash_server/hash_server --config ./hash_server/hash_config.json
# On instance of 172.18.0.10
./dns_server --id 1 --cluster http://172.18.0.10:12379 --port 9121
# On instance of 172.18.0.11
./dns_server --id 1 --cluster http://172.18.0.11:12379 --port 9121
# On instance of 172.18.0.5
curl -d "google.com. IN A 10.0.0.1" -X PUT http://172.18.0.6:9121/add
curl -d "amazon.com. IN A 10.0.0.2" -X PUT http://172.18.0.6:9121/add
dig @172.18.0.6 google.com A
dig @172.18.0.6 amazon.com A
# If you try digging each independent clusters,
# You'll likely notice that the google.com record is only present on 172.18.0.11
# and amazon.com only on 172.18.0.10
# Also try deleting
curl -d '{"name":"google.com.","rrType":"A"}' -X PUT http://172.18.0.6:9121/delete

# Broadcast example
curl -d "*.google.com. IN A 10.0.0.10" -X PUT http://172.18.0.6:9121/add
dig @172.18.0.6 www.google.com A
```