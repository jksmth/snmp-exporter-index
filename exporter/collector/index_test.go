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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/prometheus/snmp_exporter/config"
	"github.com/prometheus/snmp_exporter/scraper"
)

func TestIndexSuffixes(t *testing.T) {
	cases := []struct {
		name    string
		raw     []string
		want    []string
		wantErr bool
	}{
		{name: "absent", raw: nil, want: nil},
		{name: "blank", raw: []string{"", "  ", ","}, want: nil},
		{name: "repeated and comma", raw: []string{"42, 43", "42", "7.9"}, want: []string{"42", "43", "7.9"}},
		{name: "trimmed", raw: []string{" 42 "}, want: []string{"42"}},
		{name: "list and range", raw: []string{"1,3,6,10-12,1003"}, want: []string{"1", "3", "6", "10", "11", "12", "1003"}},
		{name: "range spaces and duplicates", raw: []string{"10 - 12, 11", "12"}, want: []string{"10", "11", "12"}},
		{name: "leading zeros", raw: []string{"007,010-012"}, want: []string{"7", "10", "11", "12"}},
		{name: "single point range", raw: []string{"10-10"}, want: []string{"10"}},
		{name: "negative", raw: []string{"-1"}, wantErr: true},
		{name: "descending", raw: []string{"20-10"}, wantErr: true},
		{name: "open range", raw: []string{"10-"}, wantErr: true},
		{name: "dotted range", raw: []string{"1.10-1.20"}, wantErr: true},
		{name: "trailing dot", raw: []string{"42."}, wantErr: true},
		{name: "name", raw: []string{"eth0"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IndexSuffixes(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIndexSuffixesCap(t *testing.T) {
	got, err := IndexSuffixes([]string{"1-256"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxIndexSuffixes || got[0] != "1" || got[len(got)-1] != "256" {
		t.Fatalf("got len %d first %q last %q", len(got), got[0], got[len(got)-1])
	}
	if _, err := IndexSuffixes([]string{"1-257"}); err == nil {
		t.Fatal("expected a range past the cap to fail")
	}
	got, err = IndexSuffixes([]string{"1-200", "150-256"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxIndexSuffixes || got[0] != "1" || got[len(got)-1] != "256" {
		t.Fatalf("overlapping ranges: len %d first %q last %q", len(got), got[0], got[len(got)-1])
	}
	if _, err := IndexSuffixes([]string{"1-200", "150-257"}); err == nil {
		t.Fatal("expected the overlapping range past the cap to fail")
	}
}

func ifIndexMetric(name, oid, typ string) *config.Metric {
	return &config.Metric{
		Name: name,
		Oid:  oid,
		Type: typ,
		Indexes: []*config.Index{{
			Labelname: "ifIndex",
			Type:      "gauge",
		}},
		Lookups: []*config.Lookup{{
			Labels:    []string{"ifIndex"},
			Labelname: "ifName",
			Oid:       "1.3.6.1.2.1.31.1.1.1.1",
			Type:      "DisplayString",
		}},
	}
}

func TestModuleForIndexesInterfaceRow(t *testing.T) {
	retries := 3
	src := &config.Module{
		Walk: []string{"1.3.6.1.2.1.2", "1.3.6.1.2.1.31.1.1"},
		Get:  make([]string, 1, 8),
		WalkParams: config.WalkParams{
			Retries: &retries,
		},
		Filters: []config.DynamicFilter{{
			Oid:     "1.3.6.1.2.1.2.2.1.7",
			Targets: []string{"1.3.6.1.2.1.2.2.1.8"},
			Values:  []string{"1"},
		}},
		Metrics: []*config.Metric{
			{Name: "ifNumber", Oid: "1.3.6.1.2.1.2.1", Type: "gauge"},
			{Name: "sysUpTime", Oid: "1.3.6.1.2.1.1.3.0", Type: "gauge"},
			ifIndexMetric("ifDescr", "1.3.6.1.2.1.2.2.1.2", "DisplayString"),
			ifIndexMetric("ifHCInOctets", "1.3.6.1.2.1.31.1.1.1.6", "counter"),
			ifIndexMetric("ifName", "1.3.6.1.2.1.31.1.1.1.1", "DisplayString"),
			{
				Name: "ifStackStatus",
				Oid:  "1.3.6.1.2.1.31.1.2.1.3",
				Type: "gauge",
				Indexes: []*config.Index{
					{Labelname: "ifStackHigherLayer", Type: "gauge"},
					{Labelname: "ifStackLowerLayer", Type: "gauge"},
				},
			},
		},
	}
	src.Get[0] = "1.2.3.0"

	dst, applied, err := ModuleForIndexes(src, []string{"42", "43"})
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("expected the interface metrics to match")
	}
	// Suffixes are applied per metric, and a lookup OID that is also a metric
	// is fetched once.
	want := []string{
		"1.2.3.0",
		"1.3.6.1.2.1.2.1.0",
		"1.3.6.1.2.1.1.3.0",
		"1.3.6.1.2.1.2.2.1.2.42",
		"1.3.6.1.2.1.31.1.1.1.1.42",
		"1.3.6.1.2.1.2.2.1.2.43",
		"1.3.6.1.2.1.31.1.1.1.1.43",
		"1.3.6.1.2.1.31.1.1.1.6.42",
		"1.3.6.1.2.1.31.1.1.1.6.43",
	}
	if !reflect.DeepEqual(dst.Get, want) {
		t.Fatalf("gets:\n got %v\nwant %v", dst.Get, want)
	}
	if len(dst.Walk) != 0 || len(dst.Filters) != 0 {
		t.Fatalf("scoped module still has walks %v or filters %v", dst.Walk, dst.Filters)
	}
	for _, oid := range dst.Get {
		if oid == "1.3.6.1.2.1.31.1.2.1.3.42" || oid == "1.3.6.1.2.1.2" {
			t.Fatalf("unexpected oid %s", oid)
		}
	}
	if dst.WalkParams.Retries == src.WalkParams.Retries {
		t.Fatal("retries pointer is shared with the loaded module")
	}
	if *dst.WalkParams.Retries != 3 {
		t.Fatalf("retries = %d", *dst.WalkParams.Retries)
	}

	if len(src.Walk) != 2 || src.Walk[0] != "1.3.6.1.2.1.2" {
		t.Fatalf("source walks mutated: %v", src.Walk)
	}
	if len(src.Get) != 1 || cap(src.Get) != 8 || src.Get[0] != "1.2.3.0" {
		t.Fatalf("source gets mutated: %v cap %d", src.Get, cap(src.Get))
	}
	if len(src.Filters) != 1 {
		t.Fatal("source filters mutated")
	}
}

func TestModuleForIndexesLeavesUnrelatedModule(t *testing.T) {
	src := &config.Module{
		Walk: []string{"1.3.6.1.2.1.1"},
		Metrics: []*config.Metric{
			{Name: "sysUpTime", Oid: "1.3.6.1.2.1.1.3", Type: "gauge"},
		},
	}
	dst, applied, err := ModuleForIndexes(src, []string{"42"})
	if err != nil {
		t.Fatal(err)
	}
	if applied || dst != src {
		t.Fatalf("scalar module should be reused unchanged, applied=%v", applied)
	}

	compound := &config.Module{
		Walk: []string{"1.3.6.1.2.1.31.1.2"},
		Metrics: []*config.Metric{{
			Name: "ifStackStatus",
			Oid:  "1.3.6.1.2.1.31.1.2.1.3",
			Type: "gauge",
			Indexes: []*config.Index{
				{Labelname: "ifStackHigherLayer", Type: "gauge"},
				{Labelname: "ifStackLowerLayer", Type: "gauge"},
			},
		}},
	}
	dst, applied, err = ModuleForIndexes(compound, []string{"42"})
	if err != nil {
		t.Fatal(err)
	}
	if applied || dst != compound {
		t.Fatal("a single integer suffix must not rewrite a two-index table")
	}

	dst, applied, err = ModuleForIndexes(compound, []string{"1.2"})
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("expected the compound index to match")
	}
	want := []string{"1.3.6.1.2.1.31.1.2.1.3.1.2"}
	if !reflect.DeepEqual(dst.Get, want) {
		t.Fatalf("got %v", dst.Get)
	}
}

func TestModuleForIndexesAddressTypeColumn(t *testing.T) {
	src := &config.Module{
		Metrics: []*config.Metric{{
			Name: "ipAdEntAddr",
			Oid:  "1.3.6.1.2.1.4.20.1.1",
			Type: "InetAddress",
			Indexes: []*config.Index{{
				Labelname: "ipAdEntIfIndex",
				Type:      "Integer32",
			}},
			Lookups: []*config.Lookup{
				{
					Labels:    []string{"ipAdEntIfIndex"},
					Labelname: "ifDescr",
					Oid:       "1.3.6.1.2.1.2.2.1.2",
				},
				{
					Labels:    []string{"ipAdEntIfIndex", "other"},
					Labelname: "partial",
					Oid:       "1.2.3.4.5",
				},
			},
		}},
	}
	dst, applied, err := ModuleForIndexes(src, []string{"42"})
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("expected a match")
	}
	want := []string{
		"1.3.6.1.2.1.4.20.1.1.42",
		"1.3.6.1.2.1.2.2.1.2.42",
		"1.3.6.1.2.1.4.20.1.0.42",
	}
	if !reflect.DeepEqual(dst.Get, want) {
		t.Fatalf("got %v, want %v", dst.Get, want)
	}
}

func TestModuleForIndexesFetchesSiblingColumn(t *testing.T) {
	if len(combinedTypeMapping) == 0 {
		t.Fatal("combinedTypeMapping is empty")
	}
	for typ := range combinedTypeMapping {
		t.Run(typ, func(t *testing.T) {
			src := &config.Module{
				Metrics: []*config.Metric{{
					Name: "value",
					Oid:  "1.3.6.1.2.1.4.20.1.1",
					Type: typ,
					Indexes: []*config.Index{{
						Labelname: "idx",
						Type:      "gauge",
					}},
				}},
			}
			dst, applied, err := ModuleForIndexes(src, []string{"42"})
			if err != nil {
				t.Fatal(err)
			}
			if !applied {
				t.Fatal("expected a match")
			}
			const sibling = "1.3.6.1.2.1.4.20.1.0.42"
			for _, oid := range dst.Get {
				if oid == sibling {
					return
				}
			}
			t.Fatalf("GET list %v missing sibling %s", dst.Get, sibling)
		})
	}
}

func TestModuleForIndexesRejectsBadSuffix(t *testing.T) {
	src := &config.Module{
		Metrics: []*config.Metric{ifIndexMetric("ifDescr", "1.3.6.1.2.1.2.2.1.2", "DisplayString")},
	}
	if _, _, err := ModuleForIndexes(src, []string{"eth0"}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestInstanceSuffixMatches(t *testing.T) {
	one := &config.Metric{Indexes: []*config.Index{{Labelname: "ifIndex", Type: "gauge"}}}
	two := &config.Metric{Indexes: []*config.Index{
		{Labelname: "a", Type: "gauge"},
		{Labelname: "b", Type: "gauge"},
	}}
	scalar := &config.Metric{}
	if !instanceSuffixMatches([]int{15}, one) {
		t.Fatal("one integer index should match one sub-identifier")
	}
	if instanceSuffixMatches([]int{1, 5}, one) {
		t.Fatal("a longer suffix must not match a one-integer index")
	}
	if !instanceSuffixMatches([]int{1, 42}, two) {
		t.Fatal("two integer indexes should match two sub-identifiers")
	}
	if instanceSuffixMatches([]int{1, 42, 0}, two) {
		t.Fatal("a trailing sub-identifier must not match")
	}
	if !instanceSuffixMatches([]int{0}, scalar) {
		t.Fatal("a scalar keeps its .0 instance")
	}
}

func TestMalformedDot3WalkReturnsUniqueSeries(t *testing.T) {
	module := duplexModule()
	body := scrapeText(t, module, nil, duplexWalk())
	duplex := series(body, "dot3StatsDuplexStatus")
	if len(duplex) != 2 {
		t.Fatalf("duplex series = %d\n%s", len(duplex), body)
	}
	got := map[string]string{}
	for _, line := range duplex {
		idx, value := labelAndValue(t, line)
		got[idx] = value
	}
	if got["15"] != "3" || got["33"] != "1" {
		t.Fatalf("duplex rows = %v", got)
	}
	if _, ok := got["1"]; ok {
		t.Fatalf("ifIndex 1 duplex was emitted from a longer suffix:\n%s", body)
	}
	descr := series(body, "ifDescr")
	if len(descr) != 3 {
		t.Fatalf("ifDescr series = %d\n%s", len(descr), body)
	}
}

func TestMalformedDot3IndexPathStaysExact(t *testing.T) {
	module, applied, err := ModuleForIndexes(duplexModule(), []string{"1", "15"})
	if err != nil {
		t.Fatal(err)
	}
	if !applied || len(module.Walk) != 0 {
		t.Fatalf("indexed module walks = %v applied = %v", module.Walk, applied)
	}
	gets := map[string]gosnmp.SnmpPDU{}
	for _, idx := range []string{"1", "15"} {
		name := map[string]string{"1": "lo", "15": "ath0"}[idx]
		for _, oid := range []string{oidIfDescr, oidIfName, oidIfAlias} {
			gets[oid+"."+idx] = octetPDU(oid+"."+idx, name)
		}
	}
	gets[oidDuplex+".15"] = integerPDU(oidDuplex+".15", 3)
	body, mock := scrapeTextWithMock(t, module, gets, nil)
	if len(mock.CallWalk()) != 0 {
		t.Fatalf("indexed scrape walked %v", mock.CallWalk())
	}
	duplex := series(body, "dot3StatsDuplexStatus")
	if len(duplex) != 1 {
		t.Fatalf("duplex series = %d\n%s", len(duplex), body)
	}
	idx, value := labelAndValue(t, duplex[0])
	if idx != "15" || value != "3" {
		t.Fatalf("duplex = ifIndex %s value %s", idx, value)
	}
	names := map[string]string{}
	for _, line := range series(body, "ifName") {
		i, _ := labelAndValue(t, line)
		names[i] = line
	}
	if _, ok := names["1"]; !ok {
		t.Fatalf("indexed scrape dropped ifIndex 1\n%s", body)
	}
	if strings.Contains(body, `ifIndex="33"`) {
		t.Fatalf("indexed scrape returned an index it was not asked for\n%s", body)
	}
}

const (
	oidIfDescr = "1.3.6.1.2.1.2.2.1.2"
	oidIfName  = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfAlias = "1.3.6.1.2.1.31.1.1.1.18"
	oidDuplex  = "1.3.6.1.2.1.10.7.2.1.19"
)

func duplexModule() *config.Module {
	indexes := []*config.Index{{Labelname: "ifIndex", Type: "gauge"}}
	lookups := []*config.Lookup{
		{Labels: []string{"ifIndex"}, Labelname: "ifAlias", Oid: oidIfAlias, Type: "DisplayString"},
		{Labels: []string{"ifIndex"}, Labelname: "ifDescr", Oid: oidIfDescr, Type: "DisplayString"},
		{Labels: []string{"ifIndex"}, Labelname: "ifName", Oid: oidIfName, Type: "DisplayString"},
	}
	column := func(name, oid, typ string) *config.Metric {
		return &config.Metric{Name: name, Oid: oid, Type: typ, Help: name, Indexes: indexes, Lookups: lookups}
	}
	return &config.Module{
		Walk:       []string{oidIfDescr, oidIfName, oidIfAlias, oidDuplex},
		WalkParams: config.DefaultWalkParams,
		Metrics: []*config.Metric{
			column("ifDescr", oidIfDescr, "DisplayString"),
			column("ifName", oidIfName, "DisplayString"),
			column("ifAlias", oidIfAlias, "DisplayString"),
			column("dot3StatsDuplexStatus", oidDuplex, "gauge"),
		},
	}
}

// duplexWalk is the uap table: three real interfaces, twelve duplex OIDs
// shaped dot3StatsDuplexStatus.1.<n>, and two exact ifIndex rows that a
// working agent would have returned in the same walk.
func duplexWalk() map[string][]gosnmp.SnmpPDU {
	names := []struct {
		index string
		name  string
	}{{"1", "lo"}, {"15", "ath0"}, {"33", "br-trunk"}}
	walk := map[string][]gosnmp.SnmpPDU{}
	for _, oid := range []string{oidIfDescr, oidIfName, oidIfAlias} {
		rows := make([]gosnmp.SnmpPDU, 0, len(names))
		for _, row := range names {
			value := row.name
			if oid == oidIfAlias {
				value = ""
			}
			rows = append(rows, octetPDU(oid+"."+row.index, value))
		}
		walk[oid] = rows
	}
	values := []int{0, 1, 2, 3, 64, 0, 1, 2, 3, 64, 7, 128}
	malformed := make([]gosnmp.SnmpPDU, 0, len(values)+2)
	for i, value := range values {
		malformed = append(malformed, integerPDU(oidDuplex+".1."+strconv.Itoa(i+1), value))
	}
	malformed = append(malformed, integerPDU(oidDuplex+".15", 3), integerPDU(oidDuplex+".33", 1))
	walk[oidDuplex] = malformed
	return walk
}

func octetPDU(oid, value string) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: "." + oid, Type: gosnmp.OctetString, Value: value}
}

func integerPDU(oid string, value int) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: "." + oid, Type: gosnmp.Integer, Value: value}
}

type staticScraper struct {
	c      *Collector
	module *NamedModule
	snmp   scraper.SNMPScraper
}

func (s staticScraper) Describe(ch chan<- *prometheus.Desc) { s.c.Describe(ch) }

func (s staticScraper) Collect(ch chan<- prometheus.Metric) {
	s.c.collect(ch, slog.New(slog.DiscardHandler), s.snmp, s.module)
}

func scrapeText(t *testing.T, module *config.Module, get map[string]gosnmp.SnmpPDU, walk map[string][]gosnmp.SnmpPDU) string {
	t.Helper()
	body, _ := scrapeTextWithMock(t, module, get, walk)
	return body
}

func scrapeTextWithMock(t *testing.T, module *config.Module, get map[string]gosnmp.SnmpPDU, walk map[string][]gosnmp.SnmpPDU) (string, *scraper.MockSNMPScraper) {
	t.Helper()
	mock := scraper.NewMockSNMPScraper(get, walk)
	c := New(context.Background(), "172.30.0.122", "public_v2c", "", "", &config.Auth{Version: 2}, nil, slog.New(slog.DiscardHandler), testMetrics(), 1, false)
	reg := prometheus.NewRegistry()
	reg.MustRegister(staticScraper{c: c, module: NewNamedModule("if_index", module), snmp: mock})
	rr := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/snmp", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", rr.Code, rr.Body.String())
	}
	return rr.Body.String(), mock
}

func testMetrics() Metrics {
	return Metrics{
		SNMPInflight:          prometheus.NewGauge(prometheus.GaugeOpts{Name: "snmp_request_in_flight"}),
		SNMPPackets:           prometheus.NewCounter(prometheus.CounterOpts{Name: "snmp_packets_total"}),
		SNMPRetries:           prometheus.NewCounter(prometheus.CounterOpts{Name: "snmp_packet_retries_total"}),
		SNMPDuration:          prometheus.NewHistogram(prometheus.HistogramOpts{Name: "snmp_packet_duration_seconds"}),
		SNMPUnexpectedPduType: prometheus.NewCounter(prometheus.CounterOpts{Name: "snmp_unexpected_pdu_type_total"}),
	}
}

func series(body, name string) []string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+"{") {
			lines = append(lines, line)
		}
	}
	return lines
}

func labelAndValue(t *testing.T, line string) (string, string) {
	t.Helper()
	idx := ""
	if i := strings.Index(line, `ifIndex="`); i >= 0 {
		rest := line[i+len(`ifIndex="`):]
		idx = rest[:strings.Index(rest, `"`)]
	}
	fields := strings.Fields(line)
	if idx == "" || len(fields) < 2 {
		t.Fatalf("cannot parse %q", line)
	}
	return idx, fields[len(fields)-1]
}
