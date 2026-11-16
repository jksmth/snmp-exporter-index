# Walk versus one index

Measured 2026-11-16 11:39 GMT by `make bench`, against the simulator on loopback with no added delay.
The agent implements the standard ifTable and ifXTable columns, plus dot3StatsDuplexStatus.
snmp_exporter walks with GetBulk and max-repetitions 25, one packet at a time.

Loopback hides the cost of those packets. A device that answers in 20 ms, with one packet in flight, spends `packets × 20 ms` waiting. That column is arithmetic on the measured packet count, not a second lab.

| Profile | Interfaces | Module | Index | Packets | PDUs | Prometheus text | Loopback | At 20 ms RTT |
| --- | ---: | --- | --- | ---: | ---: | ---: | ---: | ---: |
| edge | 48 | `if_mib` | — | 80 | 1,969 | 162,124 B | 13 ms | 1.6 s |
| edge | 48 | `if_mib` | 44 | 2 | 42 | 11,792 B | 0.5 ms | 40 ms |
| edge | 48 | `if_index` | — | 22 | 528 | 44,989 B | 4.7 ms | 440 ms |
| edge | 48 | `if_index` | 44 | 1 | 11 | 3,270 B | 0.2 ms | 20 ms |
| edge | 48 | `if_index,system` | 44 | 2 | 18 | 4,808 B | 0.4 ms | 40 ms |
| core | 1024 | `if_mib` | — | 1,681 | 41,985 | 3,497,879 B | 273 ms | 33.6 s |
| core | 1024 | `if_mib` | 134 | 2 | 42 | 11,997 B | 0.9 ms | 40 ms |
| core | 1024 | `if_index` | — | 451 | 11,264 | 983,657 B | 75 ms | 9.0 s |
| core | 1024 | `if_index` | 134 | 1 | 11 | 3,339 B | 0.3 ms | 20 ms |
| core | 1024 | `if_index,system` | 134 | 2 | 18 | 4,878 B | 0.3 ms | 40 ms |
| core | 8192 | `if_mib` | — | 13,436 | 335,873 | 29,367,318 B | 2.6 s | 4m 29s |
| core | 8192 | `if_mib` | 134 | 2 | 42 | 11,997 B | 1.3 ms | 40 ms |
| core | 8192 | `if_index` | — | 3,608 | 90,112 | 8,252,434 B | 645 ms | 1m 12s |
| core | 8192 | `if_index` | 134 | 1 | 11 | 3,339 B | 0.4 ms | 20 ms |
| core | 8192 | `if_index,system` | 134 | 2 | 18 | 4,879 B | 0.7 ms | 40 ms |
| subscriber | 8192 | `if_mib` | — | 13,436 | 335,873 | 35,184,045 B | 2.7 s | 4m 29s |
| subscriber | 8192 | `if_mib` | 2000008187 | 2 | 42 | 12,871 B | 1.2 ms | 40 ms |
| subscriber | 8192 | `if_index` | — | 3,608 | 90,112 | 9,871,975 B | 716 ms | 1m 12s |
| subscriber | 8192 | `if_index` | 2000008187 | 1 | 11 | 3,579 B | 0.6 ms | 20 ms |
| subscriber | 8192 | `if_index,system` | 2000008187 | 2 | 18 | 5,127 B | 0.5 ms | 40 ms |

A core device with 8,192 interfaces took 13,436 packets to walk `if_mib` and returned 335,873 PDUs (29,367,318 bytes of Prometheus text). The same module with `index=134` (ae0.100) took 2 packets and 42 PDUs, 6,718 times fewer packets. At 20 ms per packet that walk is 4m 29s; the single index is 40 ms.

## One interface, from the edge-switch run

This is the `if_index` module with `index` set, not a walk. The full exposition is `bench/sample_if_index.prom`.

```text
dot3StatsDuplexStatus{ifAlias="",ifDescr="ge-0/0/42",ifIndex="44",ifName="ge-0/0/42"} 3
ifHCInOctets{ifAlias="",ifDescr="ge-0/0/42",ifIndex="44",ifName="ge-0/0/42"} 4.6187344e+07
ifName{ifAlias="",ifDescr="ge-0/0/42",ifIndex="44",ifName="ge-0/0/42"} 1
ifOperStatus{ifAlias="",ifDescr="ge-0/0/42",ifIndex="44",ifName="ge-0/0/42"} 1
snmp_scrape_duration_seconds{module="if_index"} 0.000242174
snmp_scrape_packets_retried{module="if_index"} 0
snmp_scrape_packets_sent{module="if_index"} 1
snmp_scrape_pdus_returned{module="if_index"} 11
snmp_scrape_walk_duration_seconds{module="if_index"} 0.000176431
```

Regenerate with `make bench`. Packet counts should match. Loopback times, and the byte size of the text, move with the machine.
