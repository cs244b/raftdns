from scapy.all import *
import time
import queue
import json
import threading
import requests

from constants import NUM_THREADS
from constants import rec_types

def empty_request_q(
        request_q, n_requests, n_requests_lock, nameserver,
        request_timeout = 2, put_port = 9121, verbose=False):
    request_count, fail_count = 0, 0
    url = 'http://{}:{}/add'.format(nameserver, put_port)
    record_form = '{} IN {} {}'
    while True:
        try:
            domain, rec_type, answer = request_q.get(True, 1)
        except queue.Empty:
            break
        record = record_form.format(domain, rec_types[rec_type], answer)
        if verbose:
            print('THREAD: sending request to {} with {}'.format(url, record))
        try:
            res = requests.put(url, data=record, timeout=request_timeout)
        except requests.exceptions.Timeout as e:
            fail_count += 1 
        if res != None and res.status_code > 299: # 200 - 299 are success
            # request failed
            fail_count += 1
        request_count += 1
        request_q.task_done()
    with n_requests_lock:
        n_requests['count'] += request_count
    if verbose:
        print('THREAD: Exiting, sent {} requests and failed {} times'
                .format(request_count, fail_count))

def fill_request_q(request_q, traffic_file, verbose=False):
    packets = PcapReader(traffic_file)
    count = 0
    for packet in packets:
        if packet.haslayer('DNS') and packet.haslayer('DNSQR') and packet.haslayer('DNSRR'):
            dns = packet['DNS']
            rec_type = packet['DNSQR'].qtype
            qname = packet['DNSQR'].qname.decode("utf-8")
            answer = packet['DNSRR'].rdata
            answer = dns.an[dns.ancount - 1].rdata
            request_q.put((qname, rec_type, answer))
            count += 1
    if verbose:
        print('MASTER: finished filling request queue with {} requests'.format(count))

def populate_dns_server(nameserver, traffic_file, put_port = 9121, 
        taciturn=False, verbose=False):
    start_time = time.time()
    if taciturn or verbose: print('\t====== Filling Server with Files ======')
    if taciturn or verbose: print('\t\treading in file {}'.format(traffic_file))
    threads = []
    request_q = queue.Queue()
    n_queries, n_queries_lock = {'count': 0}, threading.Lock()
    master = threading.Thread(
            target=fill_request_q, 
            args=[request_q, traffic_file],
            kwargs={'verbose': verbose}
            )
    master.start()
    threads.append(master)
    for _ in range(NUM_THREADS):
        slave = threading.Thread(
                target=empty_request_q,
                args=[request_q, n_queries, n_queries_lock, nameserver],
                kwargs={'put_port': put_port, 'verbose': verbose}
                )
        slave.start()
        threads.append(slave)
    for t in threads:
        t.join()
    run_time = time.time() - start_time
    if verbose: print('\t====== Finished Simulation of Traffic ======')
    if verbose: print('\t\tAdded {} records in {} seconds'.format(
        n_queries['count'], run_time))

if __name__=='__main__':
    print('ERROR: Run this program with run.py')
