# IPv6 POC - VLAN Changes Made (Revert After POC)

## VLAN 66 Created
- Added to ALL switches via `manage_vlans.sh add` on ns2.den080.eksalab.net
- VLAN name: `ipv6-poc(fd00:80:66::/64)`
- To revert: `echo '66 ipv6-poc(fd00:80:66::/64)' > vlans.txt && ./manage_vlans.sh remove`

## Port Moves: VLAN 8 → VLAN 66

### serv10 (10.80.99.13) - Cabinet 5 (Supermicro SYS-510P-M)
| Port | Server | Original VLAN | Changed To |
|------|--------|---------------|------------|
| Eth1/9 | eksa-dev43 data | 8 (Dev) | 66 (ipv6-poc) |
| Eth1/11 | eksa-dev44 data | 8 (Dev) | 66 (ipv6-poc) |
| Eth1/13 | eksa-dev45 data | 8 (Dev) | 66 (ipv6-poc) |

Revert command (on serv10):
```
configure terminal
interface Ethernet1/9
  switchport access vlan 8
exit
interface Ethernet1/11
  switchport access vlan 8
exit
interface Ethernet1/13
  switchport access vlan 8
exit
exit
copy running-config startup-config
```

### serv5 (10.80.99.8) - Cabinet 3 (Dell R340)
| Port | Server | Original VLAN | Changed To |
|------|--------|---------------|------------|
| Eth1/24 | eksa-dev12 data | 8 (Dev) | 66 (ipv6-poc) |
| Eth1/26 | eksa-dev13 data | 8 (Dev) | 66 (ipv6-poc) |

Revert command (on serv5):
```
configure terminal
interface Ethernet1/24
  switchport access vlan 8
exit
interface Ethernet1/26
  switchport access vlan 8
exit
exit
copy running-config startup-config
```

### serv6 (10.80.99.9) - Cabinet 3 (HPE DL160)
| Port | Server | Original VLAN | Changed To |
|------|--------|---------------|------------|
| Eth1/20 | eksa-dev22 data | 8 (Dev) | 66 (ipv6-poc) |
| Eth1/22 | eksa-dev23 data | 8 (Dev) | 66 (ipv6-poc) |

Revert command (on serv6):
```
configure terminal
interface Ethernet1/20
  switchport access vlan 8
exit
interface Ethernet1/22
  switchport access vlan 8
exit
exit
copy running-config startup-config
```

### serv8 (10.80.99.11) - Cabinet 4 (Dell R750)
| Port | Server | Original VLAN | Changed To |
|------|--------|---------------|------------|
| Eth1/22 | eksa-dev31 sfp1 data | 8 (Dev) | 66 (ipv6-poc) |

Revert command (on serv8):
```
configure terminal
interface Ethernet1/22
  switchport access vlan 8
exit
exit
copy running-config startup-config
```

### serv7 (10.80.99.10) - Cabinet 4 (Dell R750 integrated NIC)
| Port | Server | Original VLAN | Changed To |
|------|--------|---------------|------------|
| Eth1/6 | eksa-dev31 rj45-p1 (integrated NIC) | 8 (Dev) | 66 (ipv6-poc) |

Revert command (on serv7):
```
configure terminal
interface Ethernet1/6
  switchport access vlan 8
exit
exit
copy running-config startup-config
```

## MLD/IGMP Snooping Changes (all switches)

Disabled link-local groups suppression for VLAN 66 on dist1, dist2, serv5, serv6, serv8, serv10:
```
configure terminal
vlan configuration 66
  no ip igmp snooping link-local-groups-suppression
end
copy running-config startup-config
```

**Why**: Default "Link Local Groups Suppression" prevents IPv6 multicast (ff02::1 RAs)
from being flooded to ports without MLD Join. PXE ROMs can't do MLD, so they never
receive Router Advertisements. Disabling this for VLAN 66 allows RA flooding.

Revert (on each switch):
```
configure terminal
vlan configuration 66
  ip igmp snooping link-local-groups-suppression
end
copy running-config startup-config
```

## BMC Ports - NOT Changed (VLAN 12)
All BMC/IPMI ports remain on VLAN 12 (DevMgt) — no changes needed.

## Notes
- eksa-dev43 BMC became unreachable after IPv6 was enabled in BMC settings (not server BIOS). Needs physical reset or IPMI link-local access from VLAN 12.
- All switches accessed via ns2.den080.eksalab.net (admin/1234qwer) using SSH key-based auth to switches.
- Config last saved dates: serv10 (May 27 2026), serv5 (May 27 2026), serv6 (May 27 2026).

## Additional: radvd on admin VM
- Installed radvd on rahulgab-dev (10.80.8.61)
- Config at /etc/radvd.conf (sends RAs on ens224 for fd00:80:66::/64)
- To stop: `sudo systemctl stop radvd && sudo systemctl disable radvd`

## Additional: ESXi port group
- Added port group "ipv6-poc" (VLAN 66) on ESXi vSwitch
- Added second vNIC (ens224) to rahulgab-dev VM with fd00:80:66::2/64
- Netplan config at /etc/netplan/00-installer-config.yaml
