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

package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

func TestDeviceShapes(t *testing.T) {
	core, err := NewDevice(48, "core")
	if err != nil {
		t.Fatal(err)
	}
	if len(core.Ifaces) != 48 {
		t.Fatalf("core interfaces = %d", len(core.Ifaces))
	}
	if core.Ifaces[0].Name != "lo0" || core.Ifaces[0].Index != 1 {
		t.Fatalf("core starts at %+v", core.Ifaces[0])
	}
	if !strings.Contains(core.Sample.Name, ".") {
		t.Fatalf("core sample should be a subinterface, got %s", core.Sample.Name)
	}
	if len(core.Objects) != 8+42*48 {
		t.Fatalf("objects = %d", len(core.Objects))
	}

	edge, err := NewDevice(48, "edge")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(edge.Sample.Name, "ge-") {
		t.Fatalf("edge sample = %s", edge.Sample.Name)
	}
	half := 0
	for _, row := range edge.Ifaces {
		if row.Duplex == duplexHalf {
			half++
		}
	}
	if half != 1 {
		t.Fatalf("half-duplex ports = %d", half)
	}

	sub, err := NewDevice(10, "subscriber")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Ifaces[0].Name != "xe-0/0/0" || sub.Sample.Index < subscriberBase {
		t.Fatalf("subscriber sample = %+v", sub.Sample)
	}
	if !strings.HasPrefix(sub.Sample.Name, "demux0.pp0.") {
		t.Fatalf("subscriber name = %s", sub.Sample.Name)
	}

	if _, err := NewDevice(0, "core"); err == nil {
		t.Fatal("expected interfaces < 1 to fail")
	}
	if _, err := NewDevice(1, "chassis"); err == nil {
		t.Fatal("expected an unknown profile to fail")
	}
}

func TestWalkAndGet(t *testing.T) {
	const n = 10
	dev, err := NewDevice(n, "core")
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{Device: dev, Community: "public"}
	if err := agent.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	go agent.Serve()

	client := dial(t, agent.Addr())
	defer client.Conn.Close()

	descr, err := client.BulkWalkAll("1.3.6.1.2.1.2.2.1.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(descr) != n {
		t.Fatalf("ifDescr rows = %d", len(descr))
	}
	for i, row := range dev.Ifaces {
		if got := pduString(descr[i]); got != row.Descr {
			t.Fatalf("ifDescr[%d] = %q, want %s", i, got, row.Descr)
		}
	}

	ifTable, err := client.BulkWalkAll("1.3.6.1.2.1.2")
	if err != nil {
		t.Fatal(err)
	}
	// ifNumber plus the 22 ifTable columns.
	if len(ifTable) != 1+22*n {
		t.Fatalf("interfaces walk = %d, want %d", len(ifTable), 1+22*n)
	}
	ifX, err := client.BulkWalkAll("1.3.6.1.2.1.31.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ifX) != 19*n {
		t.Fatalf("ifXTable walk = %d", len(ifX))
	}
	duplex, err := client.BulkWalkAll("1.3.6.1.2.1.10.7.2.1.19")
	if err != nil {
		t.Fatal(err)
	}
	if len(duplex) != n {
		t.Fatalf("duplex rows = %d", len(duplex))
	}

	name := ".1.3.6.1.2.1.31.1.1.1.1." + strconv.FormatUint(uint64(dev.Sample.Index), 10)
	got, err := client.Get([]string{name})
	if err != nil {
		t.Fatal(err)
	}
	if pduString(got.Variables[0]) != dev.Sample.Name {
		t.Fatalf("GET %s = %q, want %s", name, pduString(got.Variables[0]), dev.Sample.Name)
	}

	missing, err := client.Get([]string{".1.3.6.1.2.1.2.2.1.2.999"})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Variables[0].Type != gosnmp.NoSuchInstance {
		t.Fatalf("missing instance type = %v", missing.Variables[0].Type)
	}
}

func TestWrongCommunityIsSilent(t *testing.T) {
	dev, err := NewDevice(2, "edge")
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{Device: dev, Community: "public"}
	if err := agent.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	go agent.Serve()

	host, port := splitAddr(t, agent.Addr())
	client := &gosnmp.GoSNMP{
		Target: host, Port: port, Community: "nope", Version: gosnmp.Version2c,
		Timeout: 300 * time.Millisecond, Retries: 0,
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Conn.Close()
	if _, err := client.Get([]string{".1.3.6.1.2.1.1.1.0"}); err == nil {
		t.Fatal("wrong community got a response")
	}
}

func dial(t *testing.T, addr string) *gosnmp.GoSNMP {
	t.Helper()
	host, port := splitAddr(t, addr)
	client := &gosnmp.GoSNMP{
		Target: host, Port: port, Community: "public", Version: gosnmp.Version2c,
		Timeout: 2 * time.Second, Retries: 1, MaxRepetitions: 5,
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	return client
}

func splitAddr(t *testing.T, addr string) (string, uint16) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return host, uint16(n)
}

func pduString(pdu gosnmp.SnmpPDU) string {
	switch v := pdu.Value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}
