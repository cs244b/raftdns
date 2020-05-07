FROM golang

RUN mkdir -p /go/src/app
WORKDIR /go/src/app

# CMD ["./raftexample", "--id", "1", "--cluster", "http://0.0.0.0:12379", "--port", "12380"]

COPY . /go/src/app

RUN go build -o dns_server
RUN go build -o hash_server hash_server/hash_server.go

# Get dig, ifconfig etc.
RUN apt-get update && apt-get install -yq dnsutils && apt-get install -yq net-tools
RUN apt-get install -yq vim
RUN go get github.com/go-delve/delve/cmd/dlv
# && apt-get clean && rm -rf /var/lib/apt/lists
