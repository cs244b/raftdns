#!python 3
import sys
import time
from simulate_dns_traffic import simulate_dns_traffic
from populate_dns_server import populate_dns_server

from config import SERVER_IP
from config import SMALL_TRAFFIC_FILE
from config import MEDIUM_TRAFFIC_FILE
from config import LARGE_TRAFFIC_FILE
from config import HUGE_TRAFFIC_FILE

def parseArgs():
    if len(sys.argv) < 4:
        print("Usage \n\t\trun.py [traffic_file] [server] [traffic_case]\n")
        raise("ERROR: need 4 arguments")
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
    nameserver = SERVER_IP
    if len(sys.argv) > 2:
        if sys.argv[2] == 'ours':
            nameserver = SERVER_IP
        if sys.argv[2] == 'google':
            nameserver = '8.8.8.8'
        if sys.argv[2] == 'cloudflare':
            nameserver = '1.1.1.1'
    traffic_case = 'file'
    if len(sys.argv) > 3:
        if sys.argv[3] == 'file':
            traffic_case = 'file'
        if sys.argv[3] == 'single':
            traffic_case = 'single'
        if sys.argv[3] == 'uniform':
            traffic_case = 'uniform'
    return nameserver, traffic_file, traffic_case

if __name__=='__main__':
    nameserver, traffic_file, traffic_case = parseArgs()
    print('\n ============ POPULATING {} file on {} nameserver============\n'
            .format(traffic_file, nameserver, traffic_case))
    populate_dns_server(nameserver, traffic_file, taciturn=True, verbose=False)
    print('\n ============ SIMULATING {} file on {} nameserver in use case: {} ============\n'
            .format(traffic_file, nameserver, traffic_case))
    simulate_dns_traffic(nameserver, traffic_file, traffic_case, 
            verbose=False, taciturn=True, save_results=True)
    
