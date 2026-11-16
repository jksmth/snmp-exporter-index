// Copyright 2026 The snmp-exporter-index authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command ifsim is an SNMPv2c agent with a configurable interface table.
// It exists so a walk of IF-MIB can be compared with a single-index GET
// without a router that has thousands of interfaces.
package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	ifTypeEthernet = 6
	ifTypePPP      = 23
	ifTypeLoopback = 24
	ifTypeL2VLAN   = 135
	ifTypeLAG      = 161

	statusUp   = 1
	statusDown = 2

	duplexUnknown = 1
	duplexHalf    = 2
	duplexFull    = 3

	// Subscriber sessions use a high ifIndex, the way a BRAS does, instead of
	// packing sessions into 1..N.
	subscriberBase uint32 = 2_000_000_000

	// dot3StatsDuplexStatus is EtherLike-MIB, indexed by ifIndex.
	dot3StatsDuplexStatusOID = "1.3.6.1.2.1.10.7.2.1.19"
)

type iface struct {
	Index      uint32
	Name       string
	Descr      string
	Alias      string
	Type       int
	Mtu        int
	Speed      uint32
	HighSpeed  uint32
	Phys       []byte
	Admin      int
	Oper       int
	LastChange uint32
	InOctets   uint64
	OutOctets  uint64
	InPkts     uint64
	OutPkts    uint64
	InErrors   uint32
	OutErrors  uint32
	Duplex     int
	Connector  int
	Promisc    int
	Trap       int
}

// Device is one simulated chassis. Objects are the SNMP instances it answers,
// sorted by OID. Sample is the interface a benchmark should address when it
// wants one row that is not the first.
type Device struct {
	Profile    string
	Interfaces int
	Ifaces     []iface
	Sample     iface
	SysDescr   string
	Objects    []object
}

type object struct {
	id  []uint32
	pdu gosnmp.SnmpPDU
}

type colDef struct {
	prefix string
	ber    gosnmp.Asn1BER
	value  func(iface) any
}

func NewDevice(n int, profile string) (*Device, error) {
	if n < 1 {
		return nil, fmt.Errorf("interfaces must be at least 1")
	}
	ifaces, err := buildIfaces(n, profile)
	if err != nil {
		return nil, err
	}
	dev := &Device{
		Profile:    profile,
		Interfaces: n,
		Ifaces:     ifaces,
		Sample:     pickSample(profile, ifaces),
		SysDescr:   fmt.Sprintf("snmp-exporter-index simulator, profile %s, %d interfaces", profile, n),
	}
	dev.Objects = dev.instances()
	slices.SortFunc(dev.Objects, func(a, b object) int {
		return cmpOID(a.id, b.id)
	})
	for i := 1; i < len(dev.Objects); i++ {
		if cmpOID(dev.Objects[i-1].id, dev.Objects[i].id) >= 0 {
			return nil, fmt.Errorf("OID table is not strictly increasing at %s", dev.Objects[i].pdu.Name)
		}
	}
	return dev, nil
}

func (d *Device) instances() []object {
	objs := make([]object, 0, 8+len(columns)*len(d.Ifaces))
	objs = append(objs, d.scalars()...)
	for _, col := range columns {
		// The uap profile does not implement duplex as <column>.<ifIndex>.
		if d.Profile == "uap" && col.prefix == dot3StatsDuplexStatusOID {
			continue
		}
		prefix := parseOID(col.prefix)
		for _, iface := range d.Ifaces {
			id := append(append([]uint32{}, prefix...), iface.Index)
			name := formatOID(id)
			objs = append(objs, object{id: id, pdu: gosnmp.SnmpPDU{Name: name, Type: col.ber, Value: col.value(iface)}})
		}
	}
	if d.Profile == "uap" {
		objs = append(objs, uapDot3()...)
	}
	return objs
}

func (d *Device) scalars() []object {
	sysName := map[string]string{"edge": "edge-1", "subscriber": "bras-1"}[d.Profile]
	if sysName == "" {
		sysName = "core-1"
	}
	type scalar struct {
		oid   string
		ber   gosnmp.Asn1BER
		value any
	}
	rows := []scalar{
		{"1.3.6.1.2.1.1.1.0", gosnmp.OctetString, d.SysDescr},
		{"1.3.6.1.2.1.1.2.0", gosnmp.ObjectIdentifier, ".1.3.6.1.4.1.8072.3.2.10"},
		{"1.3.6.1.2.1.1.3.0", gosnmp.TimeTicks, uint32(12_345_600)},
		{"1.3.6.1.2.1.1.4.0", gosnmp.OctetString, "noc@example.net"},
		{"1.3.6.1.2.1.1.5.0", gosnmp.OctetString, sysName},
		{"1.3.6.1.2.1.1.6.0", gosnmp.OctetString, "lab"},
		{"1.3.6.1.2.1.1.7.0", gosnmp.Integer, 78},
		{"1.3.6.1.2.1.2.1.0", gosnmp.Integer, d.Interfaces},
	}
	objs := make([]object, 0, len(rows))
	for _, row := range rows {
		id := parseOID(row.oid)
		objs = append(objs, object{id: id, pdu: gosnmp.SnmpPDU{Name: formatOID(id), Type: row.ber, Value: row.value}})
	}
	return objs
}

// columns is the standard ifTable, ifXTable, and dot3StatsDuplexStatus.
// if_mib walks ifTable and ifXTable. The smaller if_index module also reads
// duplex, which shares ifIndex but is not part of those two walks.
var columns = []colDef{
	{"1.3.6.1.2.1.2.2.1.1", gosnmp.Integer, func(i iface) any { return int(i.Index) }},
	{"1.3.6.1.2.1.2.2.1.2", gosnmp.OctetString, func(i iface) any { return i.Descr }},
	{"1.3.6.1.2.1.2.2.1.3", gosnmp.Integer, func(i iface) any { return i.Type }},
	{"1.3.6.1.2.1.2.2.1.4", gosnmp.Integer, func(i iface) any { return i.Mtu }},
	{"1.3.6.1.2.1.2.2.1.5", gosnmp.Gauge32, func(i iface) any { return i.Speed }},
	{"1.3.6.1.2.1.2.2.1.6", gosnmp.OctetString, func(i iface) any { return i.Phys }},
	{"1.3.6.1.2.1.2.2.1.7", gosnmp.Integer, func(i iface) any { return i.Admin }},
	{"1.3.6.1.2.1.2.2.1.8", gosnmp.Integer, func(i iface) any { return i.Oper }},
	{"1.3.6.1.2.1.2.2.1.9", gosnmp.TimeTicks, func(i iface) any { return i.LastChange }},
	{"1.3.6.1.2.1.2.2.1.10", gosnmp.Counter32, func(i iface) any { return uint32(i.InOctets) }},
	{"1.3.6.1.2.1.2.2.1.11", gosnmp.Counter32, func(i iface) any { return uint32(i.InPkts) }},
	{"1.3.6.1.2.1.2.2.1.12", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.2.2.1.13", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.2.2.1.14", gosnmp.Counter32, func(i iface) any { return i.InErrors }},
	{"1.3.6.1.2.1.2.2.1.15", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.2.2.1.16", gosnmp.Counter32, func(i iface) any { return uint32(i.OutOctets) }},
	{"1.3.6.1.2.1.2.2.1.17", gosnmp.Counter32, func(i iface) any { return uint32(i.OutPkts) }},
	{"1.3.6.1.2.1.2.2.1.18", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.2.2.1.19", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.2.2.1.20", gosnmp.Counter32, func(i iface) any { return i.OutErrors }},
	{"1.3.6.1.2.1.2.2.1.21", gosnmp.Gauge32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.2.2.1.22", gosnmp.ObjectIdentifier, func(iface) any { return ".0.0" }},
	{"1.3.6.1.2.1.31.1.1.1.1", gosnmp.OctetString, func(i iface) any { return i.Name }},
	{"1.3.6.1.2.1.31.1.1.1.2", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.31.1.1.1.3", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.31.1.1.1.4", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.31.1.1.1.5", gosnmp.Counter32, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.31.1.1.1.6", gosnmp.Counter64, func(i iface) any { return i.InOctets }},
	{"1.3.6.1.2.1.31.1.1.1.7", gosnmp.Counter64, func(i iface) any { return i.InPkts }},
	{"1.3.6.1.2.1.31.1.1.1.8", gosnmp.Counter64, func(i iface) any { return uint64(0) }},
	{"1.3.6.1.2.1.31.1.1.1.9", gosnmp.Counter64, func(i iface) any { return uint64(0) }},
	{"1.3.6.1.2.1.31.1.1.1.10", gosnmp.Counter64, func(i iface) any { return i.OutOctets }},
	{"1.3.6.1.2.1.31.1.1.1.11", gosnmp.Counter64, func(i iface) any { return i.OutPkts }},
	{"1.3.6.1.2.1.31.1.1.1.12", gosnmp.Counter64, func(i iface) any { return uint64(0) }},
	{"1.3.6.1.2.1.31.1.1.1.13", gosnmp.Counter64, func(i iface) any { return uint64(0) }},
	{"1.3.6.1.2.1.31.1.1.1.14", gosnmp.Integer, func(i iface) any { return i.Trap }},
	{"1.3.6.1.2.1.31.1.1.1.15", gosnmp.Gauge32, func(i iface) any { return i.HighSpeed }},
	{"1.3.6.1.2.1.31.1.1.1.16", gosnmp.Integer, func(i iface) any { return i.Promisc }},
	{"1.3.6.1.2.1.31.1.1.1.17", gosnmp.Integer, func(i iface) any { return i.Connector }},
	{"1.3.6.1.2.1.31.1.1.1.18", gosnmp.OctetString, func(i iface) any { return i.Alias }},
	{"1.3.6.1.2.1.31.1.1.1.19", gosnmp.TimeTicks, func(i iface) any { return uint32(0) }},
	{"1.3.6.1.2.1.10.7.2.1.19", gosnmp.Integer, func(i iface) any { return i.Duplex }},
}

func buildIfaces(n int, profile string) ([]iface, error) {
	switch profile {
	case "core":
		return coreIfaces(n), nil
	case "edge":
		return edgeIfaces(n), nil
	case "subscriber":
		return subscriberIfaces(n), nil
	case "uap":
		return uapIfaces(n)
	default:
		return nil, fmt.Errorf("unknown profile %q (core, edge, subscriber, uap)", profile)
	}
}

func coreIfaces(n int) []iface {
	out := make([]iface, 0, n)
	add := func(row iface) {
		if len(out) < n {
			out = append(out, row)
		}
	}
	add(loopback())
	phys := 4
	switch {
	case n >= 1024:
		phys = 128
	case n >= 256:
		phys = 64
	case n >= 48:
		phys = 24
	}
	if phys > n-1 {
		phys = n - 1
	}
	for p := 0; p < phys; p++ {
		add(ethernet(uint32(len(out)+1), etName(p), 100_000, p == 7))
	}
	aes := 0
	if n-len(out) > 8 {
		aes = min(4, n-len(out))
	}
	for a := 0; a < aes; a++ {
		add(lag(uint32(len(out)+1), fmt.Sprintf("ae%d", a)))
	}
	parent := "et-0/0/0"
	parentSpeed := uint32(100_000)
	if aes > 0 {
		parent = "ae0"
		parentSpeed = 400_000
	}
	unit := 100
	for len(out) < n {
		add(subinterface(uint32(len(out)+1), fmt.Sprintf("%s.%d", parent, unit), parentSpeed))
		unit++
	}
	return out
}

func edgeIfaces(n int) []iface {
	out := make([]iface, 0, n)
	add := func(row iface) {
		if len(out) < n {
			out = append(out, row)
		}
	}
	add(loopback())
	vlans := 0
	if n > 8 {
		vlans = min(8, n/10)
	}
	phys := n - 1 - vlans
	for p := 0; p < phys; p++ {
		// One half-duplex port, so a small switch is not a single enum value.
		add(ethernet(uint32(len(out)+1), fmt.Sprintf("ge-0/0/%d", p), 1_000, p == 5))
	}
	for v := 1; len(out) < n; v++ {
		add(subinterface(uint32(len(out)+1), fmt.Sprintf("vlan.%d", v), 1_000))
	}
	return out
}

func subscriberIfaces(n int) []iface {
	out := make([]iface, 0, n)
	phys := min(4, n)
	for p := 0; p < phys; p++ {
		out = append(out, ethernet(uint32(p+1), fmt.Sprintf("xe-0/0/%d", p), 100_000, false))
	}
	for s := 0; len(out) < n; s++ {
		out = append(out, session(subscriberBase+uint32(s), s))
	}
	return out
}

func loopback() iface {
	return iface{
		Index: 1, Name: "lo0", Descr: "lo0", Type: ifTypeLoopback, Mtu: 16384,
		Admin: statusUp, Oper: statusUp, Duplex: duplexUnknown,
		Connector: 2, Promisc: 2, Trap: 1,
		InOctets: 10_000, OutOctets: 10_000, InPkts: 100, OutPkts: 100,
	}
}

func ethernet(index uint32, name string, mbps uint32, half bool) iface {
	duplex := duplexFull
	if half {
		duplex = duplexHalf
	}
	oper := statusUp
	admin := statusUp
	if index%17 == 0 {
		oper = statusDown
	}
	if index%29 == 0 {
		admin = statusDown
		oper = statusDown
	}
	return iface{
		Index: index, Name: name, Descr: name, Type: ifTypeEthernet, Mtu: 1514,
		Speed: speedBits(mbps), HighSpeed: mbps, Phys: mac(index),
		Admin: admin, Oper: oper, LastChange: index * 100, Duplex: duplex,
		Connector: 1, Promisc: 2, Trap: 1,
		InOctets: uint64(index)<<20 + 50_000, OutOctets: uint64(index)<<20 + 40_000,
		InPkts: uint64(index) * 1000, OutPkts: uint64(index) * 900,
		InErrors: index % 5, OutErrors: index % 3,
	}
}

func lag(index uint32, name string) iface {
	row := ethernet(index, name, 400_000, false)
	row.Type = ifTypeLAG
	row.Connector = 2
	row.Phys = nil
	row.Duplex = duplexFull
	return row
}

func subinterface(index uint32, name string, parentMbps uint32) iface {
	unit := name[strings.LastIndex(name, ".")+1:]
	row := ethernet(index, name, min(parentMbps, 10_000), false)
	row.Type = ifTypeL2VLAN
	row.Mtu = 1500
	row.Phys = nil
	row.Connector = 2
	row.Duplex = duplexUnknown
	row.Alias = "vlan:" + unit
	return row
}

func session(index uint32, n int) iface {
	oper := statusUp
	if n%10 == 0 {
		oper = statusDown
	}
	name := fmt.Sprintf("demux0.pp0.%d", n)
	return iface{
		Index: index, Name: name, Descr: name, Alias: fmt.Sprintf("sub:%d", n),
		Type: ifTypePPP, Mtu: 1492, Speed: speedBits(100), HighSpeed: 100,
		Admin: statusUp, Oper: oper, LastChange: uint32(n%10_000) * 100,
		Duplex: duplexUnknown, Connector: 2, Promisc: 2, Trap: 2,
		InOctets: uint64(n)*1_000_000 + 1234, OutOctets: uint64(n)*800_000 + 100,
		InPkts: uint64(n) * 100, OutPkts: uint64(n) * 80,
	}
}

// uapDot3Values are the integers published under dot3StatsDuplexStatus by
// the uap profile. 1, 2 and 3 are the duplex enum. 0, 64, 7 and 128 are not.
// Twelve values is the UAT failure: eleven "collected before" errors means
// twelve samples were folded onto one label set.
func uapDot3Values() []int {
	return []int{0, 1, 2, 3, 64, 0, 1, 2, 3, 64, 7, 128}
}

// uapDot3 is the raw column this profile walks. Every OID is
// dot3StatsDuplexStatus.1.<n>, a longer suffix than ifIndex. There is no
// dot3StatsDuplexStatus.<ifIndex>, so a GET of ifIndex 1, 15 or 33 is
// noSuchInstance. This is the shape that produced the UAT error, not a
// capture from the access point.
func uapDot3() []object {
	prefix := parseOID(dot3StatsDuplexStatusOID + ".1")
	values := uapDot3Values()
	objs := make([]object, 0, len(values))
	for i, value := range values {
		id := append(append([]uint32{}, prefix...), uint32(i+1))
		objs = append(objs, object{
			id: id,
			pdu: gosnmp.SnmpPDU{
				Name:  formatOID(id),
				Type:  gosnmp.Integer,
				Value: value,
			},
		})
	}
	return objs
}

func uapIfaces(n int) ([]iface, error) {
	if n < 3 {
		return nil, fmt.Errorf("uap profile needs at least 3 interfaces")
	}
	specials := []iface{uapLoopback(), uapAth(), uapBridge()}
	byIndex := make(map[uint32]iface, len(specials))
	for _, row := range specials {
		byIndex[row.Index] = row
	}
	out := make([]iface, 0, n)
	used := make(map[uint32]struct{}, len(specials))
	for idx := uint32(1); len(out) < n; idx++ {
		if row, ok := byIndex[idx]; ok {
			out = append(out, row)
			used[idx] = struct{}{}
			continue
		}
		pending := 0
		for _, row := range specials {
			if _, ok := used[row.Index]; !ok {
				pending++
			}
		}
		if n-len(out) <= pending {
			continue
		}
		out = append(out, uapVAP(idx))
	}
	return out, nil
}

func uapLoopback() iface {
	row := loopback()
	row.Name = "lo"
	row.Descr = "lo"
	row.Alias = ""
	row.Duplex = 0
	return row
}

func uapAth() iface {
	row := ethernet(15, "ath0", 1_000, false)
	row.Alias = ""
	row.Duplex = 0
	return row
}

func uapBridge() iface {
	row := ethernet(33, "br-trunk", 1_000, false)
	row.Alias = ""
	row.Duplex = 0
	return row
}

func uapVAP(index uint32) iface {
	row := ethernet(index, fmt.Sprintf("wlan%d", index), 1_000, false)
	row.Alias = ""
	row.Duplex = 0
	return row
}

func pickSample(profile string, ifaces []iface) iface {
	switch profile {
	case "uap":
		for _, row := range ifaces {
			if row.Name == "ath0" {
				return row
			}
		}
	case "subscriber":
		for i := len(ifaces) - 1; i >= 0; i-- {
			if ifaces[i].Index >= subscriberBase {
				return ifaces[i]
			}
		}
	case "edge":
		for i := len(ifaces) - 1; i >= 0; i-- {
			if strings.HasPrefix(ifaces[i].Name, "ge-") {
				return ifaces[i]
			}
		}
	default:
		for _, row := range ifaces {
			if strings.Contains(row.Name, ".") {
				return row
			}
		}
	}
	return ifaces[len(ifaces)-1]
}

func etName(p int) string {
	fpc := p / 8
	pic := (p % 8) / 4
	port := p % 4
	return fmt.Sprintf("et-%d/%d/%d", fpc, pic, port)
}

func mac(index uint32) []byte {
	return []byte{0x02, 0x00, byte(index >> 24), byte(index >> 16), byte(index >> 8), byte(index)}
}

func speedBits(mbps uint32) uint32 {
	bits := uint64(mbps) * 1_000_000
	if bits > 4294967295 {
		return 4294967295
	}
	return uint32(bits)
}

func (d *Device) get(name string) gosnmp.SnmpPDU {
	id, ok := parseOIDOK(name)
	if !ok {
		return noSuch(name)
	}
	i, found := slices.BinarySearchFunc(d.Objects, id, func(obj object, target []uint32) int {
		return cmpOID(obj.id, target)
	})
	if !found {
		return noSuch(formatOID(id))
	}
	return d.Objects[i].pdu
}

func (d *Device) next(name string) gosnmp.SnmpPDU {
	id, ok := parseOIDOK(name)
	if !ok {
		return endOfMib(name)
	}
	i, found := slices.BinarySearchFunc(d.Objects, id, func(obj object, target []uint32) int {
		return cmpOID(obj.id, target)
	})
	if found {
		i++
	}
	if i >= len(d.Objects) {
		return endOfMib(formatOID(id))
	}
	return d.Objects[i].pdu
}

func (d *Device) bulk(vars []gosnmp.SnmpPDU, nonRepeaters, maxRep int) []gosnmp.SnmpPDU {
	if nonRepeaters < 0 {
		nonRepeaters = 0
	}
	if nonRepeaters > len(vars) {
		nonRepeaters = len(vars)
	}
	if maxRep < 0 {
		maxRep = 0
	}
	// A real agent caps a response well below a request that asks for
	// thousands of repetitions. snmp_exporter's default is 25.
	if maxRep > 100 {
		maxRep = 100
	}
	out := make([]gosnmp.SnmpPDU, 0, len(vars))
	for i, v := range vars {
		if i < nonRepeaters {
			out = append(out, d.next(v.Name))
			continue
		}
		name := v.Name
		for range maxRep {
			pdu := d.next(name)
			out = append(out, pdu)
			if pdu.Type == gosnmp.EndOfMibView {
				break
			}
			name = pdu.Name
		}
	}
	return out
}

func noSuch(name string) gosnmp.SnmpPDU {
	if !strings.HasPrefix(name, ".") {
		name = "." + name
	}
	return gosnmp.SnmpPDU{Name: name, Type: gosnmp.NoSuchInstance}
}

func endOfMib(name string) gosnmp.SnmpPDU {
	if name == "" || !strings.HasPrefix(name, ".") {
		name = "." + strings.TrimPrefix(name, ".")
	}
	return gosnmp.SnmpPDU{Name: name, Type: gosnmp.EndOfMibView}
}

func parseOID(s string) []uint32 {
	id, ok := parseOIDOK(s)
	if !ok {
		panic("bad oid " + s)
	}
	return id
}

func parseOIDOK(s string) ([]uint32, bool) {
	s = strings.TrimPrefix(s, ".")
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	id := make([]uint32, len(parts))
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, false
		}
		id[i] = uint32(n)
	}
	return id, true
}

func formatOID(id []uint32) string {
	var b strings.Builder
	for _, n := range id {
		b.WriteByte('.')
		b.WriteString(strconv.FormatUint(uint64(n), 10))
	}
	return b.String()
}

func cmpOID(a, b []uint32) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
