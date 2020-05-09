# Makefile
#
# For saving the commands for building and testing our dns server
#
# Seperated into two sections:
# 	- LOCAL provides commands to run the dns server locally
# 	- DOCKER provides commands to run the server in a docker container

NAME:=dns_server
ARGS:=--id 1 --cluster http://127.0.0.1:12379 --port 12380

#################
##### LOCAL #####
#################

all: build run

build:
	go build -o $(NAME)

run:
	./$(NAME) $(ARGS)

test:
	curl -d "google.com. IN A 10.0.0.1" -X PUT -L http://127.0.0.1:8000/add
	dig @127.0.0.1 google.com A

##################
##### DOCKER #####
##################

IP:=172.30.100.104
LOCAL-HOST:=127.0.0.1
LOCAL-UDP:=8001
LOCAL-POST:=8000

# CNTNR-NAME:=container_name
# HOST-PORT:=8000
# CNTNR-PORT:=12380

docker-build:
	docker build --tag $(NAME):0.1 .

# runs dns server at ip $(IP)
#		binds $(LOCAL-POST) to port to send PUT requests
#		binds $(LOCAL-UDP) to port to send GET request (dig commands) dig 
docker-run:
	docker run --ip $(IP) \
		--publish $(LOCAL-POST):12380 --publish $(LOCAL-UDP):53/udp	\
		$(NAME):0.1 ./$(NAME) $(ARGS)

# run a simple test of the running server with `curl` and `dig`
docker-test:
	curl -d "google.com. IN A 10.0.0.5" -X PUT -L http://127.0.0.1:$(LOCAL-POST)/add
	dig @127.0.0.1 -p $(LOCAL-UDP) google.com A

docker-shell:
	docker run --ip 172.17.0.5 -it $(NAME):0.1 /bin/bash

# docker-inspect:
# 	docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $(CNTNR_NAME)


.PHONY: build run test docker-build docker-run docker-test docker-shell docker-inspect 
