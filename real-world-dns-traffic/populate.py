#!python 3
import sys
import time
from populate_dns_server import populate_dns_server

from config import SERVER_IP
from config import SMALL_TRAFFIC_FILE
from config import MEDIUM_TRAFFIC_FILE
from config import LARGE_TRAFFIC_FILE

def parseArgs():
    if len(sys.argv) < 2:
        print("Usage \n\t\tpopulate.py [traffic_file]\n")
        raise("ERROR: need 2 arguments")
    traffic_file = SMALL_TRAFFIC_FILE
    if len(sys.argv) > 1:
        if sys.argv[1] == 'small':
            traffic_file = SMALL_TRAFFIC_FILE
        if sys.argv[1] == 'medium':
            traffic_file = MEDIUM_TRAFFIC_FILE
        if sys.argv[1] == 'large':
            traffic_file = LARGE_TRAFFIC_FILE
    nameserver = SERVER_IP
    return nameserver, traffic_file

if __name__=='__main__':
    nameserver, traffic_file = parseArgs()
    print('\n ============ POPULATING {} file on {} nameserver============\n'
            .format(traffic_file, nameserver))
    populate_dns_server(nameserver, traffic_file, taciturn=True, verbose=False)
    
