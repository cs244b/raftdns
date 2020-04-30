# Example: kvstore on `docker`

This is an example of how to use docker to spawn a cluster of kvstore instances


# Building

```
docker build --tag raftexample:0.1 .
```
# Running

```
docker run --ip 172.30.100.104 raftexample:0.1 ./raftexample --id 1 --cluster http://0.0.0.0:12379 --port 12380
```

# Open a shell for debugging
```
docker run --ip 172.17.0.5 -it raftexample:0.1 /bin/bash
```

# Inspect container network
```
docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' container_name
```

# How to ping into the container's IP address?
Has to be done through a container

# How to set custom IP address?
```
// Create network first
docker network create --subnet=172.18.0.0/16 raftnet

docker run --net raftnet --ip 172.18.0.5 raftexample:0.1 ./raftexample --id 1 --cluster http://0.0.0.0:12379 --port 12380
```