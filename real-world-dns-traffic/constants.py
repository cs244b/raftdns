import dns.rdatatype
from config import SERVER_IP

rec_types = {
        0: 'ANY', 255: 'ALL',1: 'A', 2: 'NS', 3: 'MD', 4: 'MD', 5: 'CNAME',
        6: 'SOA', 7:  'MB',8: 'MG',9: 'MR',10: 'NULL',11: 'WKS',12: 'PTR',
        13: 'HINFO',14: 'MINFO',15: 'MX',16: 'TXT',17: 'RP',18: 'AFSDB',
        28: 'AAAA', 33: 'SRV', 38: 'A6', 39: 'DNAME', 
        46: 'RRSIG', 48: 'DNSKEY'
        }

supported_types = {
        'A': dns.rdatatype.A, 
        'NS': dns.rdatatype.NS, 
        'MX': dns.rdatatype.MX
        }

NUM_THREADS = 35

NAMESERVERS = {
        'google': '8.8.8.8',
        'cloudflare': '1.1.1.1',
        'ours': SERVER_IP
        }
