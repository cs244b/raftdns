import json
import glob
import os
import statistics

PATH = '../bundles/'

runtimes = {
        'google': [], 
        'cloudflare': [],
        'ours': []
        }
counter = 0

for filename in os.listdir(PATH):
    with open(os.path.join(PATH, filename), 'r') as f_in:
        try:
            data = json.load(f_in)
        except:
            continue
        counter += 1
        for i in range(3):
            nameserver = data['nameservers'][i]
            if (data['runtimes'][i] > 50):
                continue
            runtimes[nameserver].append(data['runtimes'][i])
for k,v in runtimes.items():
    print(k, ':', 'average: ', statistics.mean(v),' stdev: ', statistics.stdev(v))
print(counter)
