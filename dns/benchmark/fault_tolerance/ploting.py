# ploting script for fault tolerance benchmark
# time - throughput graph
# two lines: one without hash server, one with hash server

import re
import matplotlib.pyplot as plt


def main():
    # 2/2, 2/6, 2/7
    # for paper, use 2/6
    filename_w = 'w_hash_2.log'
    filename_wo = 'wo_hash_6.log'
    tps_w = []
    tps_wo = []
    ndp = 150

    format = 'Read throughput from all clients: (?P<_0>.+) responses/sec\n'
    with open(filename_w) as f:
        for line in f:
            if line == 'Test ended.\n':
                continue
            tps_w.append(float(re.findall(format, line)[0]))
    with open(filename_wo) as f:
        for line in f:
            if line == 'Test ended.\n':
                continue
            tps_wo.append(float(re.findall(format, line)[0]))

    ndp = min(ndp, len(tps_w), len(tps_wo))
    tps_w = tps_w[0:ndp]
    tps_wo = tps_wo[0:ndp]
    c_list = list(range(ndp))

    plt.plot(c_list, tps_w, c='#5bc49f', label='w/ hash server')
    plt.plot(c_list, tps_wo, c='#ff7c7c', label='w/o hash server')

    plt.title("Fault Tolerance")
    plt.xlabel('Time (sec)')
    plt.ylabel('Throughput (response / sec)')

    plt.ylim(ymin=0)
    plt.legend()
    plt.show()


main()
