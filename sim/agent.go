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
	"encoding/json"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Stats counts SNMP responses. Packets is what a client had to send.
type Stats struct {
	Interfaces int `json:"interfaces"`
	Objects    int `json:"objects"`
	Get        int `json:"get"`
	GetNext    int `json:"get_next"`
	GetBulk    int `json:"get_bulk"`
	Varbinds   int `json:"varbinds"`
	Bytes      int `json:"bytes"`
	Seq        int `json:"seq"`
}

func (s Stats) Packets() int { return s.Get + s.GetNext + s.GetBulk }

// Agent answers SNMPv2c on one UDP socket. Other versions are ignored.
type Agent struct {
	Device    *Device
	Community string
	RTT       time.Duration
	StatsPath string

	conn  *net.UDPConn
	mu    sync.Mutex
	stats Stats
}

func (a *Agent) Listen(address string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	a.conn = conn
	a.stats.Interfaces = a.Device.Interfaces
	a.stats.Objects = len(a.Device.Objects)
	return nil
}

func (a *Agent) Addr() string {
	return a.conn.LocalAddr().String()
}

func (a *Agent) Close() error {
	if a.conn == nil {
		return nil
	}
	return a.conn.Close()
}

func (a *Agent) Serve() error {
	buf := make([]byte, 65535)
	for {
		n, addr, err := a.conn.ReadFromUDP(buf)
		if err != nil {
			return err
		}
		kind, nvars, resp := a.handle(buf[:n])
		if resp == nil {
			continue
		}
		if a.RTT > 0 {
			time.Sleep(a.RTT)
		}
		if _, err := a.conn.WriteToUDP(resp, addr); err != nil {
			log.Printf("write: %v", err)
			continue
		}
		a.note(kind, nvars, len(resp))
	}
}

func (a *Agent) handle(packet []byte) (string, int, []byte) {
	dec := &gosnmp.GoSNMP{Version: gosnmp.Version2c, Community: a.Community}
	msg, err := dec.SnmpDecodePacket(packet)
	if err != nil {
		log.Printf("decode: %v", err)
		return "", 0, nil
	}
	if msg.Version != gosnmp.Version2c || msg.Community != a.Community {
		return "", 0, nil
	}
	var (
		kind string
		vars []gosnmp.SnmpPDU
	)
	switch msg.PDUType {
	case gosnmp.GetRequest:
		kind = "get"
		vars = make([]gosnmp.SnmpPDU, len(msg.Variables))
		for i, v := range msg.Variables {
			vars[i] = a.Device.get(v.Name)
		}
	case gosnmp.GetNextRequest:
		kind = "getnext"
		vars = make([]gosnmp.SnmpPDU, len(msg.Variables))
		for i, v := range msg.Variables {
			vars[i] = a.Device.next(v.Name)
		}
	case gosnmp.GetBulkRequest:
		kind = "getbulk"
		vars = a.Device.bulk(msg.Variables, int(msg.NonRepeaters), int(msg.MaxRepetitions))
	case gosnmp.SetRequest:
		msg.PDUType = gosnmp.GetResponse
		msg.Error = gosnmp.ReadOnly
		msg.ErrorIndex = 1
		out, err := msg.MarshalMsg()
		if err != nil {
			log.Printf("marshal set: %v", err)
			return "", 0, nil
		}
		return "set", 0, out
	default:
		return "", 0, nil
	}
	msg.PDUType = gosnmp.GetResponse
	msg.Variables = vars
	msg.Error = gosnmp.NoError
	msg.ErrorIndex = 0
	out, err := msg.MarshalMsg()
	if err != nil {
		log.Printf("marshal: %v", err)
		return "", 0, nil
	}
	return kind, len(vars), out
}

func (a *Agent) Snapshot() Stats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stats
}

func (a *Agent) note(kind string, varbinds, nbytes int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch kind {
	case "get":
		a.stats.Get++
	case "getnext":
		a.stats.GetNext++
	case "getbulk":
		a.stats.GetBulk++
	default:
		return
	}
	a.stats.Varbinds += varbinds
	a.stats.Bytes += nbytes
}

// WriteStats dumps the counters. The bench asks for this with SIGUSR1 so the
// request path does not touch the disk.
func (a *Agent) WriteStats() {
	if a.StatsPath == "" {
		return
	}
	a.mu.Lock()
	a.stats.Seq++
	raw, err := json.Marshal(a.stats)
	path := a.StatsPath
	a.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(raw, '\n'), 0o644)
}
