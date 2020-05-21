# How to setup full running instances on EC2

Please create a Security Group that allows exposing many of the ports.

Log into the instances by doing (assuming you uses PEM keys)
```sh
ssh -i <PEM_KEYPATH> ec2-user@[PUBLIC DNS (IPv4)]
```

# Create 2 separate clusters (each 3 machines)

Run below for each member of the cluster:
```sh
# setup steps
sudo yum update -y
sudo yum install -y golang
sudo yum install -y git

# somehow setup the go folders. And clone github repo to $GOPATH/src/
git clone https://github.com/cs244b/raftdns
cd raftdns/dns/
go build -o dns_server
```

Now run the following, each line on a separate machine (please use the actual IP addresses you have). `nohup` ensures that when you exit the program would still run (ignores `SIGHUP`). If a process goes rogue, try `ps -ef | grep dns_server` and get the PID, and then `sudo kill -9 <PID>` (or alternatively, `sudo pkill -9 dns_server` (`pkill` takes a name)).

__`sudo` is required.__

```sh
# Make sure all these servers use exactly the same Security Group
# Use local eth0 address (172.*) as cluster information (seems public IP might not work)
# Notice ID is different on different machines
sudo nohup ./dns_server --id 1 --cluster http://172.31.18.74:12379,http://172.31.29.245:12379,http://172.31.29.21:12379 --port 9121 2>&1 > /dev/null &
sudo nohup ./dns_server --id 2 --cluster http://172.31.18.74:12379,http://172.31.29.245:12379,http://172.31.29.21:12379 --port 9121 2>&1 > /dev/null &
sudo nohup ./dns_server --id 3 --cluster http://172.31.18.74:12379,http://172.31.29.245:12379,http://172.31.29.21:12379 --port 9121 2>&1 > /dev/null &
```

You can use the `Tags` feature on EC2 to tag these servers as a cluster, such that you won't forget about it (e.g. setting `CLUSTER=1` or `CLUSTER=2` to represent members of 2 different clusters). I tagged the above machines with `CLUSTER=1`

Create a second cluster just like above (with `CLUSTER=2`). For example, I have
```
sudo nohup ./dns_server --id 1 --cluster http://172.31.47.83:12379,http://172.31.44.48:12379,http://172.31.43.131:12379 --port 9121 2>&1 > /dev/null &
sudo nohup ./dns_server --id 2 --cluster http://172.31.47.83:12379,http://172.31.44.48:12379,http://172.31.43.131:12379 --port 9121 2>&1 > /dev/null &
sudo nohup ./dns_server --id 3 --cluster http://172.31.47.83:12379,http://172.31.44.48:12379,http://172.31.43.131:12379 --port 9121 2>&1 > /dev/null &
```

# Create 2 independent hash servers

Create new instances just like above. Set tag `HASH_SERVER=1` for easy remembrance.

Run below for each member of the cluster:
```sh
# setup steps
sudo yum update -y
sudo yum install -y golang
sudo yum install -y git

# somehow setup the go folders. And clone github repo to $GOPATH/src/
git clone https://github.com/cs244b/raftdns
cd raftdns/dns/hash_server
go build -o hash_server hash_server.go
```

Now create a config and name it as `config.json` under `hash_server`, like below:
(Notice the glue record can still use local eth0 addresses as long as under the same Security Group)
```json
[
  {
    "cluster": "cluster1",
    "members": [
      {
        "name": "ns1.raftdns1.com.",
        "glue": "ns1.raftdns1.com. 3600 IN A 172.31.18.74"
      },
      {
        "name": "ns2.raftdns1.com.",
        "glue": "ns2.raftdns1.com. 3600 IN A 172.31.29.245"
      },
      {
        "name": "ns3.raftdns1.com.",
        "glue": "ns3.raftdns1.com. 3600 IN A 172.31.29.21"
      }
    ]
  },
  {
    "cluster": "cluster2",
    "members": [
      {
        "name": "ns1.raftdns2.com.",
        "glue": "ns1.raftdns2.com. 3600 IN A 172.31.47.83"
      },
      {
        "name": "ns2.raftdns2.com.",
        "glue": "ns2.raftdns2.com. 3600 IN A 172.31.44.48"
      },
      {
        "name": "ns3.raftdns2.com.",
        "glue": "ns3.raftdns2.com. 3600 IN A 172.31.43.131"
      }
    ]
  }
]
```
`vim` is available on the instances.

Now runs on each of these 2
```sh
sudo nohup ./hash_server --config config.json 2>&1 > /dev/null &
```

Now you can try the following by sending read/write requests to hash_server (*using Public IPv4 address*)
```sh
# For example, this is on hash server 1
curl -d "google.com. IN A 10.0.0.1" -X PUT http://3.18.220.45:9121/add
# On hash server 2
curl -d "amazon.com. IN A 10.0.0.2" -X PUT http://18.219.31.175:9121/add

# Example the result
dig @3.18.220.45 google.com
dig @18.219.31.175 amazon.com

# You can further ensure if they are actually stored separately on different clusters by digging directly to the possibly target cluster!
```

And that's IT!
