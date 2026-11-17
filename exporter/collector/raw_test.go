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

package collector

import (
	"context"
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"

	"github.com/prometheus/snmp_exporter/scraper"
)

func TestFormatRawLine(t *testing.T) {
	cases := []struct {
		name string
		pdu  gosnmp.SnmpPDU
		want string
	}{
		{
			name: "integer",
			pdu:  gosnmp.SnmpPDU{Name: ".1.3.6.1.2.1.10.7.2.1.19.1.5", Type: gosnmp.Integer, Value: 64},
			want: "1.3.6.1.2.1.10.7.2.1.19.1.5 Integer 64",
		},
		{
			name: "text",
			pdu:  gosnmp.SnmpPDU{Name: ".1.3.6.1.2.1.2.2.1.2.1", Type: gosnmp.OctetString, Value: []byte("lo")},
			want: `1.3.6.1.2.1.2.2.1.2.1 OctetString "lo"`,
		},
		{
			name: "binary",
			pdu:  gosnmp.SnmpPDU{Name: "1.2.3", Type: gosnmp.OctetString, Value: []byte{0xff, 0x00}},
			want: "1.2.3 OctetString 0xff00",
		},
		{
			name: "missing",
			pdu:  gosnmp.SnmpPDU{Name: ".1.3.6.1.2.1.10.7.2.1.19.1", Type: gosnmp.NoSuchInstance},
			want: "1.3.6.1.2.1.10.7.2.1.19.1 NoSuchInstance",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatRawLine(tc.pdu); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRawVarbindsGetAndWalk(t *testing.T) {
	get := map[string]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.1.1.0": {Name: ".1.3.6.1.2.1.1.1.0", Type: gosnmp.OctetString, Value: []byte("ap")},
		"1.3.6.1.2.1.1.3.0": {Name: ".1.3.6.1.2.1.1.3.0", Type: gosnmp.TimeTicks, Value: uint32(10)},
	}
	walk := map[string][]gosnmp.SnmpPDU{
		"1.3.6.1.2.1.10.7.2.1.19": {
			{Name: ".1.3.6.1.2.1.10.7.2.1.19.1.1", Type: gosnmp.Integer, Value: 0},
			{Name: ".1.3.6.1.2.1.10.7.2.1.19.1.5", Type: gosnmp.Integer, Value: 64},
		},
	}
	mock := scraper.NewMockSNMPScraper(get, walk)
	got, err := RawVarbinds(context.Background(), mock, []string{"1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.3.0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || FormatRawLine(got[0]) != `1.3.6.1.2.1.1.1.0 OctetString "ap"` || FormatRawLine(got[1]) != "1.3.6.1.2.1.1.3.0 TimeTicks 10" {
		t.Fatalf("get = %v", got)
	}
	if len(mock.CallWalk()) != 0 {
		t.Fatalf("GET walked %v", mock.CallWalk())
	}

	mock = scraper.NewMockSNMPScraper(get, walk)
	got, err = RawVarbinds(context.Background(), mock, []string{"1.3.6.1.2.1.10.7.2.1.19"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || strings.Join(mock.CallWalk(), ",") != "1.3.6.1.2.1.10.7.2.1.19" {
		t.Fatalf("walk pdus %d calls %v", len(got), mock.CallWalk())
	}
	if len(mock.CallGet()) != 0 {
		t.Fatalf("walk issued GETs %v", mock.CallGet())
	}
}

func TestRawVarbindsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mock := scraper.NewMockSNMPScraper(nil, nil)
	if _, err := RawVarbinds(ctx, mock, []string{"1.3.6"}, false); err == nil {
		t.Fatal("expected the canceled context to stop the request")
	}
	if len(mock.CallGet()) != 0 {
		t.Fatalf("canceled request sent GETs %v", mock.CallGet())
	}
}
