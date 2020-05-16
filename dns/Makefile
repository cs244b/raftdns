# Makefile
#
# For saving the commands for building and testing our dns server
#
# Seperated into two sections:
# 	- LOCAL provides commands to run the dns server locally
# 	- DOCKER provides commands to run the server in a docker container

NAME:=dns_server
ARGS-SINGLE:=--cluster http://127.0.0.1:12379 --port 12380

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
	curl -d "google.com. IN A 10.0.0.1" -X PUT -L http://127.0.0.1:12380/add
	dig @127.0.0.1 google.com A

CLUSTER:=http://127.0.0.1:12379,http://127.0.0.1:22379,http://127.0.0.1:32379

cluster:
	./$(NAME) --id 1 --cluster $(CLUSTER) --port 12380 & 
	./$(NAME) --id 2 --cluster $(CLUSTER) --port 22380 &
	./$(NAME) --id 3 --cluster $(CLUSTER) --port 32380 &

##################
##### DOCKER #####
##################

IP:=172.30.100.104# IP address to run container on
LOCAL-HOST:=127.0.0.1# local host
LOCAL-UDP:=8001# local port to bind to udp port of dns server
LOCAL-POST:=8000# local port to bind to tcp port of dns server

# HOST-PORT:=8000
# CNTNR-PORT:=12380

docker-build:
	docker build --tag $(NAME):0.1 .

# runs dns server at ip $(IP)
#		binds $(LOCAL-POST) to port to send PUT requests
#		binds $(LOCAL-UDP) to port to send GET request (dig commands) dig 
docker-run:
	docker run --ip $(IP) \
		--publish $(LOCAL-POST):12380 --publish $(LOCAL-UDP):53/udp \
		--expose 53/udp --expose 12380 --expose 12379 \
		$(NAME):0.1 ./$(NAME) --id 1 $(ARGS-SINGLE)

# run a simple test of the running server with `curl` and `dig`
docker-test:
	curl -d "google.com. IN A 10.0.0.5" -X PUT -L http://127.0.0.1:$(LOCAL-POST)/add
	dig @127.0.0.1 -p $(LOCAL-UDP) google.com A

# open a shell in a cotainer running our dns docker image
docker-shell:
	docker run --ip 172.17.0.5 -it $(NAME):0.1 /bin/bash

IP-1:=172.30.100.104
IP-2:=172.30.100.204
IP-3:=172.30.100.304
DOCKER-CLUST:=http://$(IP-1):12379,http://$(IP-2):22379,http://$(IP-3):32379

### Unnecessary
# docker-build-cluster:
# 	docker build --tag $(NAME):1.1 .
# 	docker build --tag $(NAME):2.1 .
# 	docker build --tag $(NAME):3.1 .

# TODO Broken
# containers cannot communicate with one another
# 	already is on bridge topology, and it has exposed ports
docker-cluster:
	echo "docker cluster is currently broken"
	docker run --ip $(IP-1) \
		--publish 8001:12380 --publish 8011:53/udp	--expose 12379\
		$(NAME):0.1 ./$(NAME) --id 1 --cluster $(DOCKER-CLUST) --port 12380 &
	docker run --ip $(IP-2) \
		--publish 8002:22380 --publish 8012:53/udp	--expose 22379\
		$(NAME):0.1 ./$(NAME) --id 2 --cluster $(DOCKER-CLUST) --port 22380 &
	docker run --ip $(IP-3) \
		--publish 8003:32380 --publish 8013:53/udp	--expose 32379\
		$(NAME):0.1 ./$(NAME) --id 3 --cluster $(DOCKER-CLUST) --port 32380 &

# Inspecrt a docker container for its IP address
CNTNR-NAME:=container
docker-inspect:
	docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $(CNTNR_NAME)


.PHONY: build run test docker-build docker-run docker-test docker-shell docker-inspect 
