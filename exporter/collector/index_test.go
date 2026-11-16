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
	"reflect"
	"testing"

	"github.com/prometheus/snmp_exporter/config"
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
		{name: "negative", raw: []string{"-1"}, wantErr: true},
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
