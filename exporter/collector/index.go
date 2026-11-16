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
	"strconv"
	"strings"

	"github.com/prometheus/snmp_exporter/config"
)

// maxIndexSuffixes is the most rows one scrape may name. if_mib is about
// 42 GETs per index and the exporter puts 25 OIDs in a packet, so 256
// indexes is a few hundred packets. The README states this limit.
const maxIndexSuffixes = 256

// indexSuffixPattern matches an SNMP instance suffix: "42" or "1.42".
// Table columns are not contiguous, so a known index is fetched with GET of
// "<column OID>.<suffix>", not by walking a subtree.
var indexSuffixPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)

// IndexSuffixes parses the repeated "index" query parameter.
// Values may be repeated, comma-separated, or an inclusive range of one
// integer ("10-20"). Blank values are ignored so an empty form field does
// not change the scrape. The result is nil when the parameter is absent or
// blank. Integer tokens are written without leading zeros.
func IndexSuffixes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var suffixes []string
	add := func(suffix string) error {
		if _, ok := seen[suffix]; ok {
			return nil
		}
		if len(suffixes) == maxIndexSuffixes {
			return fmt.Errorf("'index' expands to more than %d suffixes", maxIndexSuffixes)
		}
		seen[suffix] = struct{}{}
		suffixes = append(suffixes, suffix)
		return nil
	}
	for _, part := range raw {
		for _, token := range strings.Split(part, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if err := appendIndexToken(token, add); err != nil {
				return nil, err
			}
		}
	}
	return suffixes, nil
}

func appendIndexToken(token string, add func(string) error) error {
	if strings.Contains(token, "-") {
		start, end, err := integerRange(token)
		if err != nil {
			return err
		}
		// A span this wide cannot fit, even when every value is new.
		if end-start >= uint64(maxIndexSuffixes) {
			return fmt.Errorf("'index' expands to more than %d suffixes", maxIndexSuffixes)
		}
		for n := start; ; n++ {
			if err := add(strconv.FormatUint(n, 10)); err != nil {
				return err
			}
			if n == end {
				return nil
			}
		}
	}
	if !indexSuffixPattern.MatchString(token) {
		return fmt.Errorf("'index' must be an integer, an inclusive range such as 10-20, or a dotted suffix, got %q", token)
	}
	if !strings.Contains(token, ".") {
		n, err := strconv.ParseUint(token, 10, 64)
		if err != nil {
			return fmt.Errorf("'index' must be an integer, an inclusive range such as 10-20, or a dotted suffix, got %q", token)
		}
		token = strconv.FormatUint(n, 10)
	}
	return add(token)
}

// integerRange parses "10-20" and "10 - 20". Both ends are included.
// The low end must be less than or equal to the high end.
func integerRange(token string) (uint64, uint64, error) {
	left, right, ok := strings.Cut(token, "-")
	if !ok || strings.Contains(right, "-") {
		return 0, 0, badIndexToken(token)
	}
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" || strings.Contains(left, ".") || strings.Contains(right, ".") {
		return 0, 0, badIndexToken(token)
	}
	start, errStart := strconv.ParseUint(left, 10, 64)
	end, errEnd := strconv.ParseUint(right, 10, 64)
	if errStart != nil || errEnd != nil {
		return 0, 0, badIndexToken(token)
	}
	if start > end {
		return 0, 0, fmt.Errorf("'index' range must run from low to high, got %q", token)
	}
	return start, end, nil
}

func badIndexToken(token string) error {
	return fmt.Errorf("'index' must be an integer, an inclusive range such as 10-20, or a dotted suffix, got %q", token)
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

// integerIndexWidth reports how many dotted components a suffix needs when
// every index is a single integer. The names are the integer arm of
// indexOidsAsString in this package: each consumes one sub-identifier.
// String and address indexes consume more than one, so a caller-supplied
// suffix does not address those rows. Keep this switch aligned with that function.
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

// scalarOID is the instance identifier of a scalar object. An SNMP GET
// addresses the instance, which is the object identifier with a trailing
// ".0". Hand-written modules sometimes already include it.
func scalarOID(oid string) string {
	if strings.HasSuffix(oid, ".0") {
		return oid
	}
	return oid + ".0"
}

// needsPreviousColumn reports a metric whose SNMP type is carried in the
// preceding column. combinedTypeMapping is the set the scraper already uses.
func needsPreviousColumn(typ string) bool {
	_, ok := combinedTypeMapping[typ]
	return ok
}

// instanceSuffixMatches reports whether indexOids is exactly the instance
// suffix declared by metric. A longer suffix is a different object that sits
// under the column OID. The collector otherwise keeps only the leading
// sub-identifiers, so those objects share one label set and Prometheus
// rejects the scrape.
//
// A metric with no indexes is a scalar. Its instance ".0" is not declared as
// an index, and this check leaves that path alone.
func instanceSuffixMatches(indexOids []int, metric *config.Metric) bool {
	if len(metric.Indexes) == 0 {
		return true
	}
	rest := indexOids
	for _, index := range metric.Indexes {
		_, _, rest = indexOidsAsString(rest, index.Type, index.FixedSize, index.Implied, index.EnumValues)
	}
	return len(rest) == 0
}
