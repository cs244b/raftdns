#!/bin/bash


for ((i=0; i<$1; i++))
do
	echo "Adding RR: domain = example-$i.com ip = 100.100.100.$i"
	curl -d "example-$i.com. IN A 100.100.100.$i" -X PUT http://$2:9121/add
done
