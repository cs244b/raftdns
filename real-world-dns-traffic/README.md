## Simulate Real World Traffic

### Overview

Using `run.py` and `run-bundle.py` you can simulate real world
DNS traffic captured in a `.pcap` or `.pcapng` file on a nameserver.

### Setup

Before using this command, you must capture DNS traffic that will be 
used in the simulation as a `.pcap` preferably.
Point to the file in a `config.py` file. Also add the IP addresses 
of the nameservers you are simulating traffic on.

Then make a `results/` directory for `run.py` runs to put results and
a `bundles/` directory for `run-bundle.py` to place results.

### Usage

Before we can simulate DNS traffic on our nameserver, we
must populte it with records.
To do so, run the command 
```
python3 populate.py large
```
This command will populate our namerver (IP of this nameserver is defined
in a config.py file as `SERVER_IP`) with the DNS records that are 
parsed from DNS query responses in `LARGE_TRAFFIC_FILE`.

After populating the nameserver with records, an example usage would be
```
python3 run-bundle.py large file
```
which simulates the dig requests captured in `LARGE_TRAFFIC_FILE` a
traffic capture file which's path is defined in `config.py` on 
all nameservers that are defined in `constants.py` (currently
this is our DNS nameserver, along with Google's and Cloudflare's.

Results from this run are `bundles/bundle-at-${time run}`.

### Options

Instead of sending all queries from the traffic file defined by the first
argument to `run-bundle.py`, there is also the option to send just one query
for the length of the file by setting `single` as the second argument, e.g.
```
python3 run-bundle.py large single
```

### Goal

The goal of these tests is to validate the ability of our nameserver to
server real world traffic comparably to other nameserves.
