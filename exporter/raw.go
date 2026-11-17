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
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/alecthomas/kingpin/v2"

	"github.com/prometheus/snmp_exporter/collector"
	"github.com/prometheus/snmp_exporter/config"
)

// rawPath serves SNMP varbinds as text. It is registered always and
// answers 404 until --snmp.raw-endpoint is set, so a normal scrape
// cannot reach it by accident.
const rawPath = "/snmp/raw"

// maxRawOIDs matches the index scrape cap. One request may name 256 OIDs.
const maxRawOIDs = 256

var (
	rawEndpoint = kingpin.Flag(
		"snmp.raw-endpoint",
		"Enable /snmp/raw, which returns SNMP varbinds as text. Off by default.",
	).Default("false").Bool()

	rawOIDPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)

	fetchRawVarbinds = collector.FetchRaw
)

func rawHandler(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	if rawEndpoint == nil || !*rawEndpoint {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	target := query.Get("target")
	if len(query["target"]) != 1 || target == "" {
		http.Error(w, "'target' parameter must be specified once", http.StatusBadRequest)
		snmpRequestErrors.Inc()
		return
	}
	authName := query.Get("auth")
	if len(query["auth"]) > 1 {
		http.Error(w, "'auth' parameter must only be specified once", http.StatusBadRequest)
		snmpRequestErrors.Inc()
		return
	}
	if authName == "" {
		authName = "public_v2"
	}
	snmpContext := query.Get("snmp_context")
	if len(query["snmp_context"]) > 1 {
		http.Error(w, "'snmp_context' parameter must only be specified once", http.StatusBadRequest)
		snmpRequestErrors.Inc()
		return
	}
	oids, walk, err := rawQuery(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		snmpRequestErrors.Inc()
		return
	}

	sc.mu.RLock()
	var (
		auth   *config.Auth
		authOk bool
	)
	if sc.C != nil {
		auth, authOk = sc.C.Auths[authName]
	}
	sc.mu.RUnlock()
	if !authOk {
		http.Error(w, fmt.Sprintf("Unknown auth '%s'", authName), http.StatusBadRequest)
		snmpRequestErrors.Inc()
		return
	}

	logger = logger.With("auth", authName, "target", target)
	pdus, err := fetchRawVarbinds(r.Context(), target, snmpContext, auth, oids, walk, logger, *debugSNMP)
	if err != nil {
		logger.Info("Error reading OIDs", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		snmpRequestErrors.Inc()
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, pdu := range pdus {
		fmt.Fprintf(w, "%s\n", collector.FormatRawLine(pdu))
	}
}

func rawQuery(query url.Values) ([]string, bool, error) {
	oids, err := rawOIDs(query["oid"])
	if err != nil {
		return nil, false, err
	}
	walk, err := rawWalk(query)
	if err != nil {
		return nil, false, err
	}
	if walk && len(oids) != 1 {
		return nil, false, fmt.Errorf("'walk=1' takes one oid")
	}
	return oids, walk, nil
}

func rawOIDs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("'oid' parameter must be specified")
	}
	if len(raw) > maxRawOIDs {
		return nil, fmt.Errorf("'oid' lists more than %d OIDs", maxRawOIDs)
	}
	oids := make([]string, 0, len(raw))
	for _, oid := range raw {
		oid = strings.TrimSpace(strings.TrimPrefix(oid, "."))
		if !rawOIDPattern.MatchString(oid) {
			return nil, fmt.Errorf("'oid' must be a dotted sequence of integers, got %q", oid)
		}
		oids = append(oids, oid)
	}
	return oids, nil
}

func rawWalk(query url.Values) (bool, error) {
	switch len(query["walk"]) {
	case 0:
		return false, nil
	case 1:
		if query.Get("walk") != "1" {
			return false, fmt.Errorf("'walk' must be 1, got %q", query.Get("walk"))
		}
		return true, nil
	default:
		return false, fmt.Errorf("'walk' parameter must only be specified once")
	}
}
