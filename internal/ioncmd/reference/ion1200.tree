ION1200# help
 ┬ arping ─ SOURCE ─ HOSTNAME ─ [ARPING-OPTS]
 ├ clear ┬ app-engine ┬ app-stats ┬ all
 │       │            │           ├ fcappid=STRING
 │       │            │           ├ uappid=STRING
 │       │            │           └ uappname=STRING
 │       │            ├ dns-cache ┬ all
 │       │            │           ├ ipv4=STRING
 │       │            │           └ uappname=STRING
 │       │            └ globals
 │       ├ app-map-dynamic ┬ all
 │       │                 └ srcv4=IPADDR|…
 │       ├ app-probe ─ prefix ─ application=STRING|…
 │       ├ connection ┬ all
 │       │            └ srcv4=STRING|…
 │       ├ controller_to_element ─ apicount
 │       ├ device ─ account-login ┬ all
 │       │                        └ user=VAL
 │       ├ dhcp ─ lease ┬ all
 │       │              └ vrf-name=VAL
 │       ├ dhcp6 ─ lease
 │       ├ dhcprelay ─ stat ┬ all
 │       │                  └ interface=VAL
 │       ├ element_to_controller ─ apicount
 │       ├ flow ─ srcv4=IPADDR|…
 │       ├ flow-arp ┬ all
 │       │          └ host=IPADDR
 │       ├ flow-neighbour ┬ all
 │       │                └ host=IPADDR
 │       ├ flows ─ srcv4=STRING|…
 │       ├ ipv6-neighbor ─ interface=VAL
 │       ├ locator-fqdn
 │       ├ qos-bwc ─ queue-snapshot ─ agent=VAL
 │       ├ routing ┬ multicast ─ lan-pim-statistics
 │       │         └ vrf-id=VAL|…
 │       ├ system ─ arp ─ interface ─ INTERFACE ─ host=IPADDR
 │       └ user-app-session ┬ all
 │                          └ user-ip=IPADDR|…
 ├ config ┬ banner ─ STRING
 │        ├ cellular ─ modem ─ MODEM-NAME ┬ radio ─ RADIO
 │        │                               └ sim ─ SIM-SLOT ┬ enter-pin ─ pin=STRING
 │        │                                                └ remove-pin
 │        ├ controller ─ cipher ─ CIPHER
 │        ├ dns ─ option ─ try-other-ns ┬ disable
 │        │                             └ enable
 │        ├ interface ─ INTERFACE ─ enabled=VAL
 │        ├ log-agent ─ sourceif ─ SOURCEIF|…
 │        ├ static ─ host ┬ add ┬ ip ─ IPADDR ─ names ─ STRING
 │        │               │     └ ip6 ─ IPADDR ─ names ─ STRING
 │        │               └ remove ┬ ip ─ IP
 │        │                        ├ ip6 ─ IP
 │        │                        └ name ─ NAME
 │        └ token ─ ion-token=STRING|…
 ├ curl ─ SOURCE ─ URL ─ [CURL-OPTS]
 ├ debug ┬ adem ┬ create ─ level=VAL
 │       │      ├ delete
 │       │      └ view
 │       ├ at-command ┬ get=VAL
 │       │            └ set=VAL
 │       ├ ave ┬ create ─ app-detection|…
 │       │     ├ delete ─ id=VAL
 │       │     └ view ┬ all
 │       │            └ id=VAL
 │       ├ bounce ─ INTERFACE
 │       ├ bw-test ─ src-interface ─ SOURCE ─ [DESTINATION] ┬ destination=IPADDR
 │       │                                                  └ destination=pcm.cgnx.net
 │       ├ controller ┬ reachability ─ INTERFACE
 │       │            └ refresh ─ SCOPE ─ version ─ STRING ─ endpoint ─ STRING
 │       ├ declaim
 │       ├ dnsservice ┬ add ─ [ADD] ┬ log-queries
 │       │            │             ├ log-queries=detailed
 │       │            │             └ log-queries=extra
 │       │            └ del-log-queries
 │       ├ flow ┬ create ─ id=NUMBER|…
 │       │      ├ delete ─ id=VAL
 │       │      └ view ┬ all
 │       │             └ id=VAL
 │       ├ ipfix ┬ create ─ id=NUMBER|…
 │       │       ├ delete ─ id=VAL
 │       │       └ view ┬ all
 │       │              └ id=VAL
 │       ├ log-agent ─ eal-file-log ┬ disable
 │       │                          └ enable
 │       ├ logging ┬ facility=VAL|…
 │       │         └ reset
 │       ├ logs ┬ dump ─ [OPTS] ┬
 │       │      │               └ FAC#1 ┬
 │       │      │                       └ FAC#2 ┬
 │       │      │                               └ FAC#3 ┬
 │       │      │                                       └ FAC#4 ┬
 │       │      │                                               └ FAC#5 ┬
 │       │      │                                                       └ FAC#6 ┬
 │       │      │                                                               └ FAC#7
 │       │      ├ follow ─ [OPTS] ┬
 │       │      │                 └ FAC#1 ┬
 │       │      │                         └ FAC#2 ┬
 │       │      │                                 └ FAC#3 ┬
 │       │      │                                         └ FAC#4 ┬
 │       │      │                                                 └ FAC#5 ┬
 │       │      │                                                         └ FAC#6 ┬
 │       │      │                                                                 └ FAC#7
 │       │      └ tail ─ [OPTS] ┬
 │       │                      └ FAC#1 ┬
 │       │                              └ FAC#2 ┬
 │       │                                      └ FAC#3 ┬
 │       │                                              └ FAC#4 ┬
 │       │                                                      └ FAC#5 ┬
 │       │                                                              └ FAC#6 ┬
 │       │                                                                      └ FAC#7
 │       ├ lqm ┬ create ─ vpn-path-id=VAL
 │       │     ├ delete
 │       │     └ view
 │       ├ performance-policy ┬ path-cache ─ path-id=STRING
 │       │                    └ reset-path-cache ─ path-id=STRING
 │       ├ poe ─ interface ─ INTERFACE ─ restart
 │       ├ probe ─ history ─ INTERFACE ─ samples=STRING
 │       ├ process ┬ ACTION ─ name=VAL
 │       │         └ ACTION ┬ all
 │       │                  └ name=VAL
 │       ├ reboot
 │       ├ routing ┬ log ┬ disable
 │       │         │     └ enable
 │       │         └ multicast ─ pimd ┬ MODULE
 │       │                            ├ all
 │       │                            └ disable ─
 │       ├ servicelink ─ logging ─ LOGGING
 │       ├ shutdown
 │       ├ spoke-ha ─ sync ─ overview
 │       ├ support-user ─ force-default
 │       ├ time ─ sync ─ FROM
 │       └ vpn ┬ resume ┬ RemoteSite=VAL
 │             │        ├ VpnID=VAL
 │             │        └ all
 │             └ suspend ┬ RemoteSite=VAL
 │                       ├ VpnID=VAL
 │                       └ all
 ├ dig ─ SOURCE ─ IPADDR ─ HOSTNAME
 ├ dig6 ─ SOURCE ─ IPADDR ─ HOSTNAME
 ├ dump ┬ adem ─ config
 │      ├ app-engine ┬ app-stats ┬ all
 │      │            │           ├ fcappid=STRING
 │      │            │           ├ uappid=STRING
 │      │            │           └ uappname=STRING
 │      │            ├ appdefs-info
 │      │            ├ dns-cache ┬ all
 │      │            │           ├ ipv4=STRING
 │      │            │           └ uappname=STRING
 │      │            ├ globals
 │      │            ├ memory-stats
 │      │            └ traffic-stats
 │      ├ app-l4-prefix ─ table ┬ all
 │      │                       └ l4Rule=VAL
 │      ├ app-probe ┬ config
 │      │           ├ flow ┬ all
 │      │           │      ├ application=STRING
 │      │           │      └ server-ip=IPADDR
 │      │           ├ prefix ─ application=STRING|…
 │      │           └ status
 │      ├ appdef ┬ config ┬ all
 │      │        │        ├ application=STRING
 │      │        │        └ type=VAL
 │      │        └ version
 │      ├ auth ┬ config ┬ all
 │      │      │        └ user=VAL
 │      │      └ status
 │      ├ banner ─ config
 │      ├ bfd ─ status ┬ all
 │      │              ├ localv4=STRING
 │      │              ├ localv6=STRING
 │      │              ├ remotev4=STRING
 │      │              ├ remotev6=STRING
 │      │              └ state=VAL
 │      ├ bootstrap-status
 │      ├ bypass-pair ─ config
 │      ├ cert-operation-state ─ info
 │      ├ cgnxinfra ┬ config
 │      │           └ status ┬
 │      │                    ├ live
 │      │                    └ store
 │      ├ config ┬ network
 │      │        └ security
 │      ├ controller ┬ cipher
 │      │            └ status
 │      ├ controller_to_element ─ apicount
 │      ├ cpu ─ info
 │      ├ device ┬ accessconfig
 │      │        ├ account-login ┬ all
 │      │        │               └ user=VAL
 │      │        ├ conntrack ─ count
 │      │        ├ date
 │      │        ├ info
 │      │        ├ status
 │      │        └ support-user ┬ fail-history
 │      │                       └ status
 │      ├ device-cert-operation-state ─ info
 │      ├ device-management ─ counters
 │      ├ dhcp-relay ┬ config
 │      │            └ stat ┬ all
 │      │                   └ interface=VAL
 │      ├ dhcp-server ┬ config ┬ all
 │      │             │        └ vrf-name=VAL
 │      │             └ status ┬ all
 │      │                      └ vrf-name=VAL
 │      ├ dhcpstat ┬ all
 │      │          └ vrf-name=VAL
 │      ├ disk ─ info
 │      ├ dnsservice ─ config ┬ all
 │      │                     ├ authoritative-config
 │      │                     ├ cache-config
 │      │                     ├ dns-forward-config
 │      │                     ├ dns-queries-metadata
 │      │                     ├ dns-rebind-config
 │      │                     ├ dns-response-overrides
 │      │                     ├ dnssec-config
 │      │                     └ domain-to-address
 │      ├ dpdk ┬ cpu
 │      │      ├ interface
 │      │      ├ port ┬ stats
 │      │      │      └ status ┬ all
 │      │      │               └ port=VAL
 │      │      ├ ports
 │      │      └ stats
 │      ├ element_to_controller ─ apicount
 │      ├ extension ┬ all
 │      │           └ namespace=VAL
 │      ├ fc ┬ all
 │      │    └ config|…
 │      ├ flow ┬ count-summary
 │      │      ├ drop-summary
 │      │      ├ ifaces
 │      │      └ stats
 │      ├ hypervisor
 │      ├ interface ┬ auth ┬ statistics ┬ all
 │      │           │      │            └ interface=VAL
 │      │           │      └ status ┬ all
 │      │           │               └ interface=VAL
 │      │           ├ config ┬ INTERFACE
 │      │           │        └ all
 │      │           └ status ┬ INTERFACE ┬
 │      │                    │           ├ details
 │      │                    │           └ module
 │      │                    └ all ┬
 │      │                          ├ details
 │      │                          └ module
 │      ├ ipfix ─ config ┬ collector-contexts ─ context=VAL|…
 │      │                ├ derived-exporters
 │      │                ├ filter-contexts ─ context=VAL|…
 │      │                ├ ipfix-overrides
 │      │                ├ prefix-filters ─ prefix=VAL|…
 │      │                ├ profiles
 │      │                └ templates ─ template=VAL|…
 │      ├ ipsecprofiles ┬ all
 │      │               └ profile=VAL
 │      ├ link-aggregation ┬ control ┬ INTERFACE
 │      │                  │         └ all
 │      │                  ├ data-aggregator
 │      │                  └ port ┬ INTERFACE
 │      │                         └ all
 │      ├ lldp ┬
 │      │      ├ config ┬ all
 │      │      │        └ interface=VAL
 │      │      ├ info ┬ all
 │      │      │      └ interface=VAL
 │      │      ├ stats ┬ all
 │      │      │       └ interface=VAL
 │      │      └ status ┬ all
 │      │               └ interface=VAL
 │      ├ log-agent ┬ config
 │      │           ├ eal ┬ conn ┬
 │      │           │     │      └ detail
 │      │           │     ├ response-time
 │      │           │     └ stats
 │      │           ├ iot-snmp ┬ config
 │      │           │          ├ device-discovery-stats ┬ all
 │      │           │          │                        └ node-ip=IPADDR
 │      │           │          └ neighbor-discovery-stats ┬ all
 │      │           │                                     └ node-ip=IPADDR
 │      │           ├ ip-mac-bindings
 │      │           └ status
 │      ├ nat ┬ counters ┬ all
 │      │     │          └ policysetstacks=VAL|…
 │      │     └ summary ┬ all
 │      │               └ policysetstacks=VAL|…
 │      ├ network-policy ─ config ┬ policy-rules ┬ all
 │      │                         │              └ application=STRING|…
 │      │                         ├ policy-sets ─ all
 │      │                         ├ policy-stacks ─ all
 │      │                         └ prefix-filters ┬ all
 │      │                                          └ prefix=STRING
 │      ├ oss-license ┬ all
 │      │             └ list
 │      ├ overview
 │      ├ performance-policy ─ config ┬ policy-rules ┬ [RULES] ┬ action-type=VAL
 │      │                             │              │         ├ app-transfer-type=VAL
 │      │                             │              │         ├ application=STRING
 │      │                             │              │         ├ path-label=STRING
 │      │                             │              │         ├ path-type=VAL
 │      │                             │              │         ├ policy-rule=STRING
 │      │                             │              │         └ service-label=STRING
 │      │                             │              └ all
 │      │                             ├ policy-set-stack ─ all
 │      │                             ├ policy-sets ┬ all
 │      │                             │             └ policy-set=STRING
 │      │                             └ threshold-profile ┬ all
 │      │                                                 └ profile=STRING
 │      ├ poe ┬
 │      │     ├ details ─ INTERFACE
 │      │     └ system ┬
 │      │              ├ config
 │      │              └ status
 │      ├ priority-policy ─ config ┬ policy-rules ┬ all
 │      │                          │              └ application=STRING|…
 │      │                          ├ policy-sets ─ all
 │      │                          ├ policy-stacks ─ all
 │      │                          └ prefix-filters ┬ all
 │      │                                           └ prefix=STRING
 │      ├ qos-bwc ─ config ┬ INTERFACE
 │      │                  └ all
 │      ├ radius ┬ config
 │      │        ├ statistics ┬ all
 │      │        │            └ interface=VAL
 │      │        └ status
 │      ├ reachability-probe ┬ config ┬ all
 │      │                    │        ├ hostname=VAL
 │      │                    │        ├ ipv4=VAL
 │      │                    │        └ type=VAL
 │      │                    └ status ┬ all
 │      │                             ├ hostname=VAL
 │      │                             ├ ipv4=VAL
 │      │                             └ type=VAL
 │      ├ routing ┬ aspath-list ┬ all
 │      │         │             └ name=STRING
 │      │         ├ cache ┬ all
 │      │         │       └ interface|…
 │      │         ├ community-info
 │      │         ├ communitylist ┬ all
 │      │         │               └ name=STRING
 │      │         ├ global-config
 │      │         ├ multicast ┬ config ┬ all
 │      │         │           │        ├ brief
 │      │         │           │        ├ interface
 │      │         │           │        ├ rp
 │      │         │           │        ├ sourcesite
 │      │         │           │        └ wanpeers
 │      │         │           ├ igmp ┬ all
 │      │         │           │      └ interface=VAL
 │      │         │           ├ interface ┬ INTERFACE
 │      │         │           │           └ all
 │      │         │           ├ mroute ┬ Type=VAL|…
 │      │         │           │        ├ all
 │      │         │           │        ├ detail ─ Type=VAL|…
 │      │         │           │        └ statistics ─ Type=VAL|…
 │      │         │           ├ pim ┬ bsrinfo
 │      │         │           │     ├ neighbor ─ Type=VAL|…
 │      │         │           │     ├ rp ┬ address=IPADDR
 │      │         │           │     │    ├ all
 │      │         │           │     │    └ dynamic
 │      │         │           │     ├ rpf
 │      │         │           │     └ secondary
 │      │         │           ├ statistics ─ Type=VAL|…
 │      │         │           └ status
 │      │         ├ peer ┬ advertised-routes ┬ all
 │      │         │      │                   └ vrf-id=VAL|…
 │      │         │      ├ config ┬ all
 │      │         │      │        └ vrf-id=VAL|…
 │      │         │      ├ neighbor-stat ─ vrf-id=VAL|…
 │      │         │      ├ received-routes ┬ all
 │      │         │      │                 └ vrf-id=VAL|…
 │      │         │      ├ route-json ┬ all
 │      │         │      │            └ vrf-id=VAL|…
 │      │         │      ├ routes ┬ all
 │      │         │      │        └ vrf-id=VAL|…
 │      │         │      └ status ┬ all
 │      │         │               └ vrf-id=VAL|…
 │      │         ├ prefix-reachability ─ prefix=STRING|…
 │      │         ├ prefixlist ┬ all
 │      │         │            └ name=STRING
 │      │         ├ route ─ prefix=STRING|…
 │      │         ├ route-via ─ vrf-id=VAL|…
 │      │         ├ routemap ┬ all
 │      │         │          └ name=STRING
 │      │         ├ running-config
 │      │         ├ static-route ┬ config ┬ all
 │      │         │              │        └ destination-prefix=VAL|…
 │      │         │              └ reachability-status ┬ all
 │      │         │                                    └ destination-prefix=VAL|…
 │      │         └ summary ─ address-family=VAL|…
 │      ├ sase ─ region-association ┬ ALL ┬
 │      │                           │     └ details
 │      │                           └ REGION ┬
 │      │                                    └ details
 │      ├ security-policy ─ config ┬ policy-rules ┬ [RULES] ┬ action=VAL
 │      │                          │              │         ├ application=STRING
 │      │                          │              │         ├ dest-zone=STRING
 │      │                          │              │         ├ policy-rule=STRING
 │      │                          │              │         ├ source-zone=STRING
 │      │                          │              │         ├ state=VAL
 │      │                          │              │         ├ user-group=STRING
 │      │                          │              │         └ user=STRING
 │      │                          │              └ all
 │      │                          ├ policy-set ┬ all
 │      │                          │            └ policy-set-name=STRING
 │      │                          ├ policy-set-stack ─ all
 │      │                          ├ prefix-filters ┬ all
 │      │                          │                └ prefix-filter=STRING
 │      │                          └ zones ┬ all
 │      │                                  └ security-zone=STRING
 │      ├ sensor ─ [FILTERS]
 │      ├ serviceendpoints ┬ all
 │      │                  └ endpoint=VAL
 │      ├ servicelink ┬ stats ┬ sldev=VAL
 │      │             │       └ slname=VAL
 │      │             ├ status ┬ sldev=VAL
 │      │             │        └ slname=VAL
 │      │             └ summary ┬ all
 │      │                       └ sltype=VAL
 │      ├ sfp ─ info
 │      ├ site ─ config
 │      ├ smartctl-data
 │      ├ snmpagent ┬ config
 │      │           ├ engine-id
 │      │           └ status
 │      ├ snmptrap ─ config
 │      ├ software ┬ config
 │      │          └ status
 │      ├ spoke-ha ┬ config
 │      │          └ status
 │      ├ standingalarms
 │      ├ static ─ host ─ config
 │      ├ static-arp ─ config ┬ INTERFACE
 │      │                     └ all
 │      ├ syslog ┬ config
 │      │        └ status
 │      ├ syslog-rtr ─ stats
 │      ├ time ┬ config
 │      │      ├ log
 │      │      └ status
 │      ├ token
 │      ├ tpm ─ status
 │      ├ troubleshoot ─ message
 │      ├ user-id ┬ group-mapping ┬ all
 │      │         │               ├ groupname=STRING
 │      │         │               └ username=STRING
 │      │         ├ groupidx ┬ all
 │      │         │          └ groupname=STRING
 │      │         ├ ip-user-mapping ┬ all
 │      │         │                 └ ipaddress=IPADDR
 │      │         ├ summary
 │      │         └ useridx ┬ all
 │      │                   └ username=STRING
 │      ├ vlan ─ member
 │      ├ vpn ┬ count
 │      │     ├ ha ─ all|…
 │      │     ├ ka ┬ VpnID=VAL
 │      │     │    ├ all
 │      │     │    └ summary
 │      │     ├ sharedsecret ─ VpnID=VAL
 │      │     ├ status ┬ VpnID=VAL
 │      │     │        └ VpnLinkID=VAL
 │      │     └ summary ─ [SUMMARY] ┬ RemoteSite=VAL|…
 │      │                           └ all
 │      ├ vrf ┬ info ┬ all
 │      │     │      ├ id=VAL
 │      │     │      └ name=VAL
 │      │     ├ profile
 │      │     └ route-leak-rule ┬ all
 │      │                       ├ id=VAL
 │      │                       └ src-vrf=VAL|…
 │      └ waninterface ┬ config
 │                     └ summary
 ├ dump-support ┬ details ─ file=STRING
 │              └ output ─ file=STRING
 ├ exit
 ├ file ┬ export ─ SOURCE ┬ FILE ─ scp://user@host[:port]:location
 │      │                 ├ core ─ FILE ─ scp://user@host[:port]:location
 │      │                 ├ log ─ FILE ─ scp://user@host[:port]:location
 │      │                 └ templog ─ FILE ─ scp://user@host[:port]:location
 │      ├ list ┬
 │      │      ├ core
 │      │      ├ log
 │      │      └ templog
 │      ├ make ─ FILE
 │      ├ remove ┬ FILE1 ┬
 │      │        │       └ FILE2 ┬
 │      │        │               └ FILE3 ┬
 │      │        │                       └ FILE4 ┬
 │      │        │                               └ FILE5 ─
 │      │        └ core ─ FILE1 ┬
 │      │                       └ FILE2 ┬
 │      │                               └ FILE3 ┬
 │      │                                       └ FILE4 ┬
 │      │                                               └ FILE5 ─
 │      ├ space ─ available
 │      ├ tailf ┬ log ─ FILE
 │      │       └ templog ─ FILE
 │      └ view ┬ FILE
 │             ├ log ─ FILE
 │             └ templog ─ FILE
 ├ help ┬ [COMMANDLINE]
 │      └ option ─ STRING
 ├ inspect ┬ app-flow-table ─ srcv4=STRING|…
 │         ├ app-l4-prefix ─ lookup ─ dstv4=STRING|…
 │         ├ app-map ┬ detail
 │         │         ├ filter ─ srcv4=IPADDR|…
 │         │         └ summary
 │         ├ app-perf-stats ─ application=STRING|…
 │         ├ certificate ┬ cic
 │         │             ├ device
 │         │             └ mic
 │         ├ cgnxinfra ─ ROLE ─ STATS-TYPE
 │         ├ connection ┬ all
 │         │            └ srcv4=STRING|…
 │         ├ dhcp6lease ┬ all
 │         │            └ subnet=STRING|…
 │         ├ dhcplease ┬ all
 │         │           └ subnet=STRING|…
 │         ├ dpdk ┬ ip-rules
 │         │      └ vrf ─ vrf=STRING
 │         ├ dump-rtmstate ┬ save ─ file=STRING
 │         │               └ show
 │         ├ fib ┬ all
 │         │     └ site-id=STRING|…
 │         ├ fib-leak ┬ all
 │         │          └ site-id=STRING|…
 │         ├ flow ┬ brief ─ srcv4=STRING|…
 │         │      ├ detail ─ srcv4=STRING|…
 │         │      └ internal ─ srcv4=STRING|…
 │         ├ flow-arp ┬ all
 │         │          ├ gateway=IPADDR
 │         │          ├ host=IPADDR
 │         │          ├ interface=VAL
 │         │          └ vlan=STRING
 │         ├ flow-neighbour ┬ all
 │         │                ├ gateway=IPADDR
 │         │                ├ host=IPADDR
 │         │                ├ interface=VAL
 │         │                └ vlan=STRING
 │         ├ interface ─ stats ┬ INTERFACE ─
 │         │                   └ all ┬
 │         │                         └ details
 │         ├ ip-rules
 │         ├ ipfix ┬ app-table ─ grep=STRING|…
 │         │       ├ collector-stats
 │         │       ├ exporter-stats
 │         │       ├ interface-info ─ grep=STRING|…
 │         │       └ wan-path-info ─ grep=STRING|…
 │         ├ ipv6 ─ mcast ─ membership ┬ all
 │         │                           └ interface=VAL
 │         ├ ipv6-rules
 │         ├ lan-network
 │         ├ lqm ─ stats ┬ all
 │         │             └ vpn|…
 │         ├ lqm-server ┬ error-counters ─ vpn-link-ids=STRING
 │         │            ├ global-counters
 │         │            ├ session-stats ─ vpn-link-ids=STRING
 │         │            ├ worker-error-counters ┬ all
 │         │            │                       └ worker-id=STRING
 │         │            └ worker-stats ┬ all
 │         │                           └ worker-id=STRING
 │         ├ memory ─ summary
 │         ├ network-policy ┬ conflicts
 │         │                ├ dropped
 │         │                ├ hits ─ policy-rules ┬ all
 │         │                │                     └ reset-diff|…
 │         │                └ lookup ─ app-wildcard|…
 │         ├ performance-policy ┬ fec ─ status ─ path-id=STRING|…
 │         │                    ├ hits ┬ all
 │         │                    │      ├ analytics ─ THRESHOLD-TYPE ┬ all
 │         │                    │      │                            └ reset-diff|…
 │         │                    │      └ flows ┬ all
 │         │                    │              └ reset-diff|…
 │         │                    ├ incidents ─ type ┬ app-quality ─ application=STRING|…
 │         │                    │                  └ link-quality ─ CIRCUIT ┬
 │         │                    │                                           ├ details
 │         │                    │                                           └ summary
 │         │                    └ lookup ─ application=STRING|…
 │         ├ policy-manager ─ status
 │         ├ policy-mix ─ lookup-flow ─ srcv4=STRING|…
 │         ├ priority-policy ┬ conflicts
 │         │                 ├ dropped
 │         │                 ├ hits ┬ default-rule-dscp ┬ all
 │         │                 │      │                   └ reset-diff|…
 │         │                 │      └ policy-rules ┬ all
 │         │                 │                     └ reset-diff|…
 │         │                 └ lookup ─ app-wildcard|…
 │         ├ process ─ status
 │         ├ qos-bwc ┬ debug-state
 │         │         ├ queue-history ─ |…
 │         │         └ queue-snapshot ─ |…
 │         ├ router-connectivity
 │         ├ routing ─ multicast ┬ fc ┬ internal ┬ err-stats
 │         │                     │    │          └ mem-stats
 │         │                     │    ├ mroute ┬
 │         │                     │    │        ├ detail ─ source=STRING|…
 │         │                     │    │        └ source=STRING|…
 │         │                     │    └ site-iface ┬
 │         │                     │                 ├ local ─ id=STRING
 │         │                     │                 ├ remote-site ─ id=STRING
 │         │                     │                 └ vpnlink ─ id=STRING
 │         │                     ├ interface
 │         │                     ├ internal ─ bss-groups
 │         │                     └ mroute ┬ cache
 │         │                              └ table
 │         ├ security-policy ┬ lookup ─ src-network-id=STRING|…
 │         │                 └ size
 │         ├ slab-allocator ─ memory
 │         ├ system ┬ arp ┬ all
 │         │        │     └ interface=VAL
 │         │        ├ ipv6-neighbour ┬ all
 │         │        │                └ interface=VAL
 │         │        └ vrf ┬ all
 │         │              └ name=VAL
 │         ├ user-app-session ┬ all
 │         │                  └ user-ip=IPADDR|…
 │         ├ vrf ┬ [TYPE] ┬ core-in
 │         │     │        ├ default
 │         │     │        └ main
 │         │     ├ all
 │         │     ├ list
 │         │     └ vrf=STRING
 │         └ wanpaths ┬ all
 │                    └ site-id=STRING|…
 ├ nslookup ─ HOSTNAME
 ├ ping ─ SOURCE ─ HOSTNAME ─ [TCPDUMP-OPTS]
 ├ ping6 ─ SOURCE ─ HOSTNAME ─ [TCPDUMP-OPTS]
 ├ set ┬ paging ─ [off]
 │     ├ prompt ─ STRING
 │     ├ timeout ─ NUMBER
 │     ├ traceback ─ [off]
 │     └ vim-mode ─ [off]
 ├ ssh ─ SOURCE ─ <user>@<hostname> ─ option-key-algo=VAL|…
 ├ ssh6 ─ SOURCE ─ <user>@<hostname> ─ option-key-algo=VAL|…
 ├ support
 ├ tcpdump ┬ INTERFACE ─ [TCPDUMP-OPTS] ┬ save ─ file=STRING
 │         │                            └ show
 │         └ any ─ [TCPDUMP-OPTS] ┬ save ─ file=STRING
 │                                └ show
 ├ tcpping ─ SOURCE ─ HOSTNAME:PORT
 ├ traceroute ─ SOURCE ─ HOSTNAME ─ [TRACEROUTE-OPTS]
 └ traceroute6 ─ SOURCE ─ HOSTNAME ─ [TRACEROUTE6-OPTS]
