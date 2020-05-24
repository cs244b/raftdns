#!python 3
from simulate_dns_traffic import simulate_dns_traffic

from config import SERVER_IP
from config import TRAFFIC_FILE
from config import LARGE_TRAFFIC_FILE

if __name__=='__main__':
    simulate_dns_traffic(SERVER_IP, TRAFFIC_FILE)
    
