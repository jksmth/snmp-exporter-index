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
	"fmt"
	"regexp"
	"strings"

	"github.com/prometheus/snmp_exporter/config"
)

// indexSuffixPattern matches an SNMP instance suffix: "42" or "1.42".
// Table columns are not contiguous, so a known index is fetched with GET of
// "<column OID>.<suffix>", not by walking a subtree.
var indexSuffixPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)

// IndexSuffixes parses the repeated "index" query parameter.
// Values may be repeated or comma-separated. Blank values are ignored so an
// empty form field does not change the scrape. The result is nil when the
// parameter is absent or blank.
func IndexSuffixes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var suffixes []string
	for _, part := range raw {
		for _, suffix := range strings.Split(part, ",") {
			suffix = strings.TrimSpace(suffix)
			if suffix == "" {
				continue
			}
			if !indexSuffixPattern.MatchString(suffix) {
				return nil, fmt.Errorf("'index' must be a dotted sequence of non-negative integers, got %q", suffix)
			}
			if _, ok := seen[suffix]; ok {
				continue
			}
			seen[suffix] = struct{}{}
			suffixes = append(suffixes, suffix)
		}
	}
	return suffixes, nil
}

// ModuleForIndexes returns a module that fetches suffixes with SNMP GET.
//
// applied is false, and src is returned unchanged, when no columnar metric
// has the same number of integer indexes as a suffix. Scalar modules such as
// "system" therefore keep a normal scrape when the request also names an
// interface index.
//
// When a metric matches, walks and dynamic filters are dropped. Scalars are
// fetched at oid.0. Lookup columns that use the whole index, and the previous
// column for types that encode a sibling, are fetched with the same suffix so
// the Prometheus labels still resolve. Metrics whose index shape does not
// match are omitted from that scrape.
//
// src is never mutated. The loaded configuration is shared across requests.
func ModuleForIndexes(src *config.Module, suffixes []string) (*config.Module, bool, error) {
	if len(suffixes) == 0 {
		return src, false, nil
	}
	for _, suffix := range suffixes {
		if !indexSuffixPattern.MatchString(suffix) {
			return nil, false, fmt.Errorf("'index' must be a dotted sequence of non-negative integers, got %q", suffix)
		}
	}

	byWidth := make(map[int][]string, len(suffixes))
	for _, suffix := range suffixes {
		width := strings.Count(suffix, ".") + 1
		byWidth[width] = append(byWidth[width], suffix)
	}

	matched := 0
	for _, metric := range src.Metrics {
		width, ok := integerIndexWidth(metric)
		if !ok {
			continue
		}
		if len(byWidth[width]) > 0 {
			matched++
		}
	}
	if matched == 0 {
		return src, false, nil
	}

	dst := *src
	if src.WalkParams.Retries != nil {
		retries := *src.WalkParams.Retries
		dst.WalkParams.Retries = &retries
	}

	seen := make(map[string]struct{})
	gets := make([]string, 0, len(src.Get)+len(src.Metrics)*len(suffixes))
	add := func(oid string) {
		if oid == "" {
			return
		}
		if _, ok := seen[oid]; ok {
			return
		}
		seen[oid] = struct{}{}
		gets = append(gets, oid)
	}
	for _, oid := range src.Get {
		add(oid)
	}
	for _, metric := range src.Metrics {
		if metric.Oid == "" {
			continue
		}
		width, ok := integerIndexWidth(metric)
		if !ok {
			if len(metric.Indexes) == 0 {
				add(scalarOID(metric.Oid))
			}
			continue
		}
		for _, suffix := range byWidth[width] {
			add(metric.Oid + "." + suffix)
			for _, lookup := range metric.Lookups {
				if lookup.Oid == "" || len(lookup.Labels) != len(metric.Indexes) {
					continue
				}
				add(lookup.Oid + "." + suffix)
			}
			if needsPreviousColumn(metric.Type) {
				add(getPrevOid(metric.Oid) + "." + suffix)
			}
		}
	}

	dst.Get = gets
	dst.Walk = nil
	dst.Filters = nil
	return &dst, true, nil
}

func integerIndexWidth(metric *config.Metric) (int, bool) {
	if len(metric.Indexes) == 0 {
		return 0, false
	}
	for _, index := range metric.Indexes {
		switch index.Type {
		case "Integer", "Integer32", "gauge", "counter":
		default:
			return 0, false
		}
	}
	return len(metric.Indexes), true
}

func scalarOID(oid string) string {
	if strings.HasSuffix(oid, ".0") {
		return oid
	}
	return oid + ".0"
}

func needsPreviousColumn(typ string) bool {
	switch typ {
	case "InetAddress", "InetAddressMissingSize", "LldpPortId":
		return true
	default:
		return false
	}
}
