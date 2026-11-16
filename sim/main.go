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
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:11611", "UDP address to answer SNMPv2c on")
	community := flag.String("community", "public", "SNMPv2c community")
	n := flag.Int("interfaces", 1024, "how many interfaces to publish")
	profile := flag.String("profile", "core", "core (router), edge (switch), subscriber (BRAS), or uap (access point)")
	rtt := flag.Duration("rtt", 0, "artificial delay before each SNMP response")
	statsPath := flag.String("stats", "", "write JSON counters to this file after each response")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ifsim [flags]\n\n")
		fmt.Fprintf(os.Stderr, "SNMPv2c agent with a full ifTable, ifXTable, and dot3StatsDuplexStatus.\n")
		fmt.Fprintf(os.Stderr, "Profiles:\n")
		fmt.Fprintf(os.Stderr, "  core         loopback, 100G ports, aggregate ethernet, then VLAN subinterfaces\n")
		fmt.Fprintf(os.Stderr, "  edge         a switch: 1G ports, a few VLAN interfaces, one half-duplex port\n")
		fmt.Fprintf(os.Stderr, "  subscriber   a few uplinks and many demux0.pp0 sessions at a high ifIndex\n")
		fmt.Fprintf(os.Stderr, "  uap          lo, ath0, br-trunk; duplex column has a longer suffix than ifIndex\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	dev, err := NewDevice(*n, *profile)
	if err != nil {
		log.Fatal(err)
	}
	agent := &Agent{
		Device:    dev,
		Community: *community,
		RTT:       *rtt,
		StatsPath: *statsPath,
	}
	if err := agent.Listen(*listen); err != nil {
		log.Fatal(err)
	}
	defer agent.Close()

	ready, err := json.Marshal(map[string]any{
		"ready":        true,
		"listen":       agent.Addr(),
		"profile":      dev.Profile,
		"interfaces":   dev.Interfaces,
		"objects":      len(dev.Objects),
		"sample_index": dev.Sample.Index,
		"sample_name":  dev.Sample.Name,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", ready)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	go func() {
		for s := range sig {
			if s == syscall.SIGUSR1 {
				agent.WriteStats()
				continue
			}
			_ = agent.Close()
			return
		}
	}()
	if err := agent.Serve(); err != nil {
		// Closing the socket to shut down is not a failure.
		if !isClosed(err) {
			log.Fatal(err)
		}
	}
}

func isClosed(err error) bool {
	return err != nil && strings.Contains(err.Error(), "closed")
}
