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
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"

	"github.com/prometheus/snmp_exporter/config"
)

func TestRawEndpointDisabled(t *testing.T) {
	old := *rawEndpoint
	*rawEndpoint = false
	t.Cleanup(func() { *rawEndpoint = old })

	rr := rawRequest(t, "/snmp/raw?target=127.0.0.1&oid=1.3.6.1.2.1.1.1.0")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestRawEndpoint(t *testing.T) {
	oldFlag := *rawEndpoint
	*rawEndpoint = true
	t.Cleanup(func() { *rawEndpoint = oldFlag })

	oldConfig := sc.C
	sc.mu.Lock()
	sc.C = &config.Config{Auths: map[string]*config.Auth{
		"public_v2": {Version: 2, Community: "public"},
	}}
	sc.mu.Unlock()
	t.Cleanup(func() {
		sc.mu.Lock()
		sc.C = oldConfig
		sc.mu.Unlock()
	})

	var gotWalk bool
	var gotOIDs []string
	var gotAuth string
	oldFetch := fetchRawVarbinds
	fetchRawVarbinds = func(_ context.Context, _ string, _ string, auth *config.Auth, oids []string, walk bool, _ *slog.Logger, _ bool) ([]gosnmp.SnmpPDU, error) {
		gotWalk = walk
		gotOIDs = append([]string(nil), oids...)
		gotAuth = string(auth.Community)
		if walk {
			return []gosnmp.SnmpPDU{
				{Name: ".1.3.6.1.2.1.10.7.2.1.19.1.5", Type: gosnmp.Integer, Value: 64},
			}, nil
		}
		return []gosnmp.SnmpPDU{
			{Name: ".1.3.6.1.2.1.1.1.0", Type: gosnmp.OctetString, Value: []byte("ap")},
			{Name: ".1.3.6.1.2.1.10.7.2.1.19.1", Type: gosnmp.NoSuchInstance},
		}, nil
	}
	t.Cleanup(func() { fetchRawVarbinds = oldFetch })

	rr := rawRequest(t, "/snmp/raw?target=127.0.0.1&oid=.1.3.6.1.2.1.1.1.0&oid=1.3.6.1.2.1.10.7.2.1.19.1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type %q", ct)
	}
	want := "1.3.6.1.2.1.1.1.0 OctetString \"ap\"\n1.3.6.1.2.1.10.7.2.1.19.1 NoSuchInstance\n"
	if rr.Body.String() != want {
		t.Fatalf("body %q", rr.Body.String())
	}
	if gotWalk || gotAuth != "public" || len(gotOIDs) != 2 || gotOIDs[0] != "1.3.6.1.2.1.1.1.0" {
		t.Fatalf("walk %v auth %q oids %v", gotWalk, gotAuth, gotOIDs)
	}

	rr = rawRequest(t, "/snmp/raw?target=127.0.0.1&auth=public_v2&oid=1.3.6.1.2.1.10.7.2.1.19&walk=1")
	if rr.Code != http.StatusOK {
		t.Fatalf("walk status %d body %s", rr.Code, rr.Body.String())
	}
	if !gotWalk || rr.Body.String() != "1.3.6.1.2.1.10.7.2.1.19.1.5 Integer 64\n" {
		t.Fatalf("walk %v body %q", gotWalk, rr.Body.String())
	}

	for _, path := range []string{
		"/snmp/raw?oid=1.3.6",
		"/snmp/raw?target=127.0.0.1&target=127.0.0.2&oid=1.3.6",
		"/snmp/raw?target=127.0.0.1",
		"/snmp/raw?target=127.0.0.1&oid=ifDescr",
		"/snmp/raw?target=127.0.0.1&oid=1.3.6&oid=1.3.6.1&walk=1",
		"/snmp/raw?target=127.0.0.1&oid=1.3.6&walk=yes",
		"/snmp/raw?target=127.0.0.1&auth=missing&oid=1.3.6",
	} {
		rr = rawRequest(t, path)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s status %d body %s", path, rr.Code, rr.Body.String())
		}
	}

	tooMany := "/snmp/raw?target=127.0.0.1"
	for i := range maxRawOIDs + 1 {
		tooMany += "&oid=1.3.6." + itoa(i)
	}
	rr = rawRequest(t, tooMany)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("cap status %d", rr.Code)
	}
}

func rawRequest(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	rawHandler(rr, httptest.NewRequest(http.MethodGet, path, nil), slog.New(slog.DiscardHandler))
	return rr
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
