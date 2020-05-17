# Makefile
#
# For saving the commands for building and testing our dns server
#
# Seperated into two sections:
# 	- LOCAL provides commands to run the dns server locally
# 	- DOCKER provides commands to run the server in a docker container

NAME:=dns_server
PUT-PORT:=12380#port to curl commands to
COMM-PORT:=12379#port for cluster communication
ARGS-SINGLE:=--cluster http://127.0.0.1:$(COMM-PORT) --port $(PUT-PORT)

#################
##### LOCAL #####
#################

all: build run

# build the dns sever
build:
	go build -o $(NAME)

# run a single dns server locally
run:
	./$(NAME) --id 1 $(ARGS-SINGLE)

# kill all instances of $(NAME)
stop:
	pkill $(NAME)

clean:
	go clean
	rm -rf raftexample*

# simple test of adding A record and digging for it
test:
	curl -d "google.com. IN A 10.0.0.1" -X PUT -L http://127.0.0.1:$(PUT-PORT)/add
	dig @127.0.0.1 google.com A

# spawn a local cluster of nodes
CLUSTER:=http://127.0.0.1:12379,http://127.0.0.1:22379,http://127.0.0.1:32379
cluster:
	./$(NAME) --id 1 --cluster $(CLUSTER) --port 12380 & 
	./$(NAME) --id 2 --cluster $(CLUSTER) --port 22380 &
	./$(NAME) --id 3 --cluster $(CLUSTER) --port 32380 &

# # TODO - doesn't seem to be able to add a new node into the cluster
# NEW-CLUSTER:=http://127.0.0.1:12379,http://127.0.0.1:22379,http://127.0.0.1:32379,http://127.0.0.1:42379
# # test cluster by adding node 4 to the cluster then killing node 3
# test-cluster:
# 	curl -L http://127.0.0.1:12380/4 -XPOST -d http://127.0.0.1:42379
# 	./$(NAME) --id 4 --cluster $(NEW-CLUSTER) --port 42380 --join
# 	# curl -L http://127.0.0.1:12380/3 -XDELETE

##################
##### DOCKER #####
##################

IP:=172.30.100.104# IP address to run container on
LOCAL-HOST:=127.0.0.1# local host
LOCAL-UDP:=8001# local port to bind to udp port of dns server
LOCAL-PUT:=8000# local port to bind to tcp port of dns server

# build and run docker container
docker: docker-build docker-run

# build docker container
docker-build:
	docker build --tag $(NAME):0.1 .

# runs dns server at ip $(IP)
#		binds $(LOCAL-PUT) to port to send PUT requests
#		binds $(LOCAL-UDP) to port to send GET request (dig commands) dig 
docker-run:
	docker run --ip $(IP) \
		--publish $(LOCAL-PUT):$(PUT-PORT) --publish $(LOCAL-UDP):53/udp \
		--expose 53/udp --expose $(PUT-PORT) --expose $(COMM-PORT) \
		$(NAME):0.1 ./$(NAME) --id 1 $(ARGS-SINGLE)

# run a simple test of the running server with `curl` and `dig`
docker-test:
	curl -d "google.com. IN A 10.0.0.5" -X PUT -L http://127.0.0.1:$(LOCAL-PUT)/add
	dig @127.0.0.1 -p $(LOCAL-UDP) google.com A

# open a shell in a cotainer running our dns docker image
docker-shell:
	docker run --ip 172.17.0.5 -it $(NAME):0.1 /bin/bash

# stops the currently running docker containers
docker-stop:
	docker container stop $$(docker container ls -aq)
	
# prunes away all docker containers that are not in use 
# (don't let the containers pile up!)
docker-prune:
	docker container prune

############################
###### DOCKER CLUSTER ######
############################

DNSNET-IP:=172.28.0.0/26#ip of dnsnet 
IP-1:=172.28.0.11#ip of node 1
IP-2:=172.28.0.22#ip of node 2
IP-3:=172.28.0.33#ip of node 3
DOCKER-CLUST:=http://$(IP-1):$(COMM-PORT),http://$(IP-2):$(COMM-PORT),http://$(IP-3):$(COMM-PORT)

# build docker and cluster and run
docker-cluster: docker-build docker-cluster-build docker-cluster-run

# run before starting 
docker-cluster-build:
	docker network create -d bridge --subnet=$(DNSNET-IP) dnsnet

docker-cluster-1:
	docker run --net dnsnet --ip $(IP-1) \
	 	--expose $(PUT-PORT) --expose 53/udp	--expose $(COMM-PORT)\
		$(NAME):0.1 ./$(NAME) --id 1 --cluster $(DOCKER-CLUST) --port $(PUT-PORT)

docker-cluster-2:
	docker run --net dnsnet --ip $(IP-2) \
	 	--expose $(PUT-PORT) --expose 53/udp	--expose $(COMM-PORT)\
		$(NAME):0.1 ./$(NAME) --id 2 --cluster $(DOCKER-CLUST) --port $(PUT-PORT)

docker-cluster-3:
	docker run --net dnsnet --ip $(IP-3) \
	 	--expose $(PUT-PORT) --expose 53/udp	--expose $(COMM-PORT)\
		$(NAME):0.1 ./$(NAME) --id 3 --cluster $(DOCKER-CLUST) --port $(PUT-PORT)

docker-cluster-run:
	make docker-cluster-1 &
	make docker-cluster-2 &
	make docker-cluster-3 &

# docker test script to be run from another docker container on dnsnet
docker-cluster-test-script:
	echo "TESTING NODE 1"
	curl -d "example.com. IN A 10.0.0.1" -XPUT -L http://$(IP-1):$(PUT-PORT)/add
	dig @$(IP-1) example.com
	echo "TESTING NODE 2"
	curl -d "example.com. IN A 10.0.0.2" -XPUT -L http://$(IP-2):$(PUT-PORT)/add
	dig @$(IP-2) example.com
	echo "TESTING NODE 3"
	curl -d "example.com. IN A 10.0.0.3" -XPUT -L http://$(IP-3):$(PUT-PORT)/add
	dig @$(IP-3) example.com

IP-TEST:=172.28.0.44
# test docker-cluster
docker-cluster-test:
	docker run --net dnsnet --ip $(IP-TEST) -it $(NAME):0.1 \
		make docker-cluster-test-script

# open bash in a container to debug cluster
docker-cluster-shell:
	docker run --net dnsnet --ip $(IP-TEST) -it $(NAME):0.1 /bin/bash

# Inspecrt a docker container for its IP address
CNTNR-NAME:=container
docker-inspect:
	docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $(CNTNR_NAME)


.PHONY: build run test docker-build docker-run docker-test docker-shell docker-inspect 
