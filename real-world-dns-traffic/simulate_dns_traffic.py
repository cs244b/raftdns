from scapy.all import *
import time # for time info
import queue
from datetime import datetime

from constants import rec_types # for deciphering query type indexes
from constants import supported_types # for deciphering query type indexes
from constants import NUM_THREADS
## for dns query creation and sending
import dns.name
import dns.query
import dns.dnssec
import dns.message
import dns.resolver
import dns.rdatatype


'''
Parses rec_type: string for its dns query type and returns the corresponding
dns.rdataype.

returns:
    dns.rdatatype associated with rec_type, -1 if unsupported type
'''
def parse_rec_type(rec_type):
    if rec_type not in rec_types:
        return -1
    rec_type = rec_types[rec_type]
    if rec_type not in supported_types:
        return -1
    return supported_types[rec_type]


def send_dns_query(domain, rec_type, name_server='8.8.8.8', max_wait_time=3, verbose=False):
    rec_type = parse_rec_type(rec_type)
    if rec_type < 0:
        return -1
    if verbose:
        print('sending query for {} {} record'.format(domain, rec_type))
    res = None
    try:
        res = dns.resolver.query(domain, rec_type)
    except:
        return -1
    # if res.rcode() != 0:
    #     return -1
    ip = res.rrset[0].to_text()
    return ip

def empty_query_q(query_q, n_queries, n_queries_lock, verbose=True):
    query_count = 0
    while True:
        try:
            domain, rec_type = query_q.get(True, 1)
        except queue.Empty:
            break
        ip = send_dns_query(domain, rec_type, verbose=verbose)
        query_count += 1
        query_q.task_done()
    with n_queries_lock:
        n_queries['count'] += query_count
        
   
def fill_query_q(query_q, traffic_file, verbose=True):
    packets = PcapReader(traffic_file)
    for packet in packets:
        if packet.haslayer(DNS):
            rec_type = packet[DNSQR].qtype
            qname = packet[DNSQR].qname.decode("utf-8")
            query_q.put((qname, rec_type))

def simulate_dns_traffic(server_ip, traffic_file, verbose=True):
    start_time = time.time()
    if verbose: print('\n ====== Beginning Simulation of Traffic ====== \n')
    if verbose: print('reading in file {}'.format(traffic_file))
    query_q = queue.Queue()
    threads = []
    n_queries, n_queries_lock = {'count': 0}, threading.Lock()
    master = threading.Thread(
            target=fill_query_q, 
            args=[query_q, traffic_file],
            kwargs={'verbose': verbose}
            )
    master.start()
    threads.append(master)
    for _ in range(NUM_THREADS):
        slave = threading.Thread(
                target=empty_query_q,
                args=[query_q, n_queries, n_queries_lock],
                kwargs={'verbose': verbose}
                )
        slave.start()
        threads.append(slave)
    for t in threads:
        t.join()
    run_time = time.time() - start_time
    if verbose: print('\n ====== Finished Simulation of Traffic ====== \n')
    if verbose: print('\tQueried {} times in {} seconds'.format(
        n_queries['count'], run_time))
    now = datetime.now()
    dt = now.strftime("%d-%m-%H:%M:%S")
    with open('final-stats-{}'.format(dt), 'w') as f_stat:
        f_stat.write('{} queries in {} seconds'.format(n_queries['count'], run_time))

if __name__=='__main__':
    print('ERROR: Run this program with run.py')

