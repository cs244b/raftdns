#!python 3
import sys
import time
import json
import random
from datetime import datetime
from simulate_dns_traffic import simulate_dns_traffic
from populate_dns_server import populate_dns_server

from config import SERVER_IP
from config import SMALL_TRAFFIC_FILE
from config import LARGE_TRAFFIC_FILE
from config import MEDIUM_TRAFFIC_FILE
from config import HUGE_TRAFFIC_FILE
from constants import NAMESERVERS

def parseArgs():
    traffic_file = SMALL_TRAFFIC_FILE
    if len(sys.argv) > 1:
        if sys.argv[1] == 'small':
            traffic_file = SMALL_TRAFFIC_FILE
        if sys.argv[1] == 'medium':
            traffic_file = MEDIUM_TRAFFIC_FILE
        if sys.argv[1] == 'large':
            traffic_file = LARGE_TRAFFIC_FILE
        if sys.argv[1] == 'huge':
            traffic_file = HUGE_TRAFFIC_FILE
    traffic_case = None
    if len(sys.argv) > 2:
        if sys.argv[2] == 'file':
            traffic_case = 'file'
        if sys.argv[2] == 'single':
            traffic_case = 'single'
        if sys.argv[2] == 'uniform':
            traffic_case = 'uniform'
        if traffic_case == None:
            raise 'You did not enter a valid traffic type'
    return traffic_file, traffic_case

if __name__=='__main__':
    traffic_file, traffic_case = parseArgs()
    print('########################################')
    print('############# RUNNING BUNDLE ###########')
    print('########################################')
    nameservers, ips = [], []
    for nameserver, ip in NAMESERVERS.items():
        nameservers.append(nameserver)
        ips.append(ip)
    choices = [i for i in range(len(nameservers))]
    counts = [0 for _ in range(len(ips))]
    runtimes = [0 for _ in range(len(ips))]
    failures = [0 for _ in range(len(ips))]
    while len(choices) > 0:
        choice = random.choice(choices) 
        nameserver, ip = nameservers[choice], ips[choice]
        choices.remove(choice)
        print('\n ###### SIMULATING {} file on {} nameserver in use case: {} #######\n'
                .format(traffic_file, nameserver, traffic_case))
        # populate_dns_server(nameserver, traffic_file, verbose=True)
        count, runtime, failure = simulate_dns_traffic(
                ip, traffic_file, traffic_case, 
                verbose=False, taciturn=True, save_results=True)
        counts[choice] = count
        runtimes[choice] = runtime
        failures[choice] = failure
    now = datetime.now()
    dt = now.strftime("%d-%m-%H:%M:%S")
    file_name = 'bundles/bundle-at-{}'.format(dt)
    with open(file_name, 'w') as f:
        f.write(json.dumps({
            'nameservers': nameservers,
            'ips': ips,
            'traffic_file': traffic_file,
            'traffic_case': traffic_case,
            'counts': counts,
            'runtimes': runtimes,
            'failures': failures
            }))
    f.close()
    print('########################################')
    print('############ FINISHED BUNDLE ###########')
    print('########################################')
