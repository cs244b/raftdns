* To populate RRs, passing *p* as type:  
  `./fault_tolerance --ip=18.144.9.6,54.241.141.210,54.67.86.37 --type=p`

* To test pure read throughput without hash server  

  * *ip* of public ip addresses of all cluster node
  * *type* of *t* that test throughput
  * *client* of client thread number
  * *duration* of testing duration
  * *intervals* of throughput output reporting interval 
  * *quiet* of less verbose mode 

  An example usage:   
    `./fault_tolerance --ip=18.144.9.6,54.241.141.210,54.67.86.37 --type=t --client=16 --duration=11 --interval=5 --quiet`  

* To test pure read throughput with hash server  

  * *useHash* to tell the script to querying hash server
  * *hashIp* of hash server ip address

  An example usage:  
  `./fault_tolerance --useHash --hashIp=54.183.63.131 --type=t --client=16 --duration=152 --interval=1 `
