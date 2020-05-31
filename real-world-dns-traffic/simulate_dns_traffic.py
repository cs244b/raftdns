from scapy.all import *
import time # for time info
import queue
import json
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


def send_dns_query(domain, rec_type, resolver, verbose=False):
    rec_type = parse_rec_type(rec_type)
    if rec_type < 0:
        return -1
    if verbose:
        print('sending query for {} {} record'.format(domain, rec_type))
    res = None
    try:
        res = resolver.query(domain, rec_type)
    except:
        return -1
    ip = res.rrset[0].to_text()
    return ip

def empty_query_q(
        query_q, n_queries, n_queries_lock, 
        name_server='8.8.8.8', query_timeout = 2, verbose=False, check_answer=False):
    query_count, fail_count = 0, 0
    resolver = dns.resolver.Resolver()
    resolver.nameservers = [name_server]
    resolver.timeout, resolver.lifetime = query_timeout, query_timeout
    while True:
        try:
            domain, rec_type, expected_ans = query_q.get(True, 1)
        except queue.Empty:
            break
        ip = send_dns_query(domain, rec_type, resolver, verbose=verbose)
        if check_answer and ip != expected_ans:
            return -1
        if ip == -1:
            fail_count += 1
        query_count += 1
        query_q.task_done()
    with n_queries_lock:
        n_queries['count'] += query_count
        n_queries['failures'] += fail_count
    if verbose:
        print('THREAD: Exiting, sent {} queries and failed {} times'
                .format(query_count, fail_count))

def parse_packet(packet):
    rec_type = packet['DNSQR'].qtype
    qname = packet['DNSQR'].qname.decode("utf-8")
    dns = packet['DNS']
    answer = None
    for i in range(dns.ancount):
        try:
            answer = dns.an[i].rdata
        except:
            pass
    return qname, rec_type, answer

        
'''
fill query queue with queries for slaves to execute.
also allows for special case tests
'''
def fill_query_q(query_q, traffic_file, traffic_case, verbose=True):
    if traffic_case == 'file':
        packets = PcapReader(traffic_file)
        for packet in packets:
            if packet.haslayer(DNS) and packet.haslayer('DNSQR') and packet.haslayer('DNSRR'): 
                query_q.put(parse_packet(packet))
    if traffic_case == 'single':
        packets = PcapReader(traffic_file)
        query, count = None, 0
        for packet in packets:
            if query == None and packet.haslayer(DNS):
                query = parse_packet(packet) 
            count += 1
        for _ in range(count):
            query_q.put(query)
        
def simulate_dns_traffic(
        nameserver_ip, traffic_file, traffic_case, 
        verbose=False, taciturn=False, save_results=False):
    if verbose or taciturn: print('\t ++ reading in file {} ++ '.format(traffic_file))
    query_q = queue.Queue()
    fill_query_q(query_q, traffic_file, traffic_case, verbose=verbose)
    if verbose or taciturn: print('\t====== Beginning Simulation of Traffic ======')
    start_time = time.time()
    n_queries, n_queries_lock = {'count': 0, 'failures': 0}, threading.Lock()
    threads = []
    for _ in range(NUM_THREADS):
        slave = threading.Thread(
                target=empty_query_q,
                args=[query_q, n_queries, n_queries_lock],
                kwargs={'verbose': verbose, 'name_server' : nameserver_ip}
                )
        slave.start()
        threads.append(slave)
    for t in threads:
        t.join()
    run_time = time.time() - start_time
    if verbose or taciturn: print('\t====== Finished Simulation of Traffic ======')
    if verbose or taciturn: print('\t\tQueried {} times in {} seconds, with {} failures'.format(
        n_queries['count'], run_time, n_queries['failures']))
    if save_results:
        now = datetime.now()
        dt = now.strftime("%d-%m-%H:%M:%S")
        file_name = 'results/stats-{}-{}-{}-{}'.format(nameserver_ip, traffic_file[16:22], traffic_case, dt)
        with open(file_name, 'w') as f_stat:
            stats = {'queries': n_queries['count'], 'failures': n_queries['failures'],
                    'run_time': run_time,
                    'traffic_file': traffic_file, 'nameserver_ip': nameserver_ip}
            f_stat.write(json.dumps(stats))
        f_stat.close()
        if verbose or taciturn:
            print('\t\tSaved results to file {}'.format(file_name))
    return n_queries['count'], run_time, n_queries['failures']


if __name__=='__main__':
    print('ERROR: Run this program with run.py')

