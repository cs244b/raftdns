FROM golang

RUN mkdir -p /go/src/app
WORKDIR /go/src/app

# this will ideally be built by the ONBUILD below ;)
CMD ["./raftexample", "--id", "1", "--cluster", "http://0.0.0.0:12379", "--port", "12380"]

COPY . /go/src/app

RUN go build -o raftexample

# Get dig, ifconfig etc.
RUN apt-get update && apt-get install -yq dnsutils && apt-get install -yq net-tools
# && apt-get clean && rm -rf /var/lib/apt/lists
