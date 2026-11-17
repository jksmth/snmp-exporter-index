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
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"

	"github.com/prometheus/snmp_exporter/config"
	"github.com/prometheus/snmp_exporter/scraper"
)

// FetchRaw GETs oids, or walks the single OID when walk is set.
// The SNMP client uses the process source address, the default packet
// timeout and retries, and ctx. Cancelling ctx stops the walk the same
// way a closed scrape request does. There is no limit on how many
// varbinds a walk may return.
func FetchRaw(ctx context.Context, target, snmpContext string, auth *config.Auth, oids []string, walk bool, logger *slog.Logger, debug bool) ([]gosnmp.SnmpPDU, error) {
	client, err := scraper.NewGoSNMP(logger, target, *srcAddress, debug)
	if err != nil {
		return nil, err
	}
	client.SetOptions(func(g *gosnmp.GoSNMP) {
		retries := 3
		if config.DefaultWalkParams.Retries != nil {
			retries = *config.DefaultWalkParams.Retries
		}
		g.Retries = retries
		g.Timeout = config.DefaultWalkParams.Timeout
		g.MaxRepetitions = config.DefaultWalkParams.MaxRepetitions
		g.Context = ctx
		auth.ConfigureSNMP(g, snmpContext)
	})
	if err := client.Connect(); err != nil {
		return nil, err
	}
	defer client.Close()
	return RawVarbinds(ctx, client, oids, walk)
}

// RawVarbinds runs the GET or walk against an existing client.
// A walk is one subtree. GETs are sent in batches of max-repetitions,
// which defaults to 25. Exception varbinds (noSuchInstance and the
// rest) are kept: the caller asked to see what the device returned.
func RawVarbinds(ctx context.Context, snmp scraper.SNMPScraper, oids []string, walk bool) ([]gosnmp.SnmpPDU, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if walk {
		if len(oids) != 1 {
			return nil, fmt.Errorf("a walk takes one OID")
		}
		return snmp.WalkAll(oids[0])
	}
	batch := int(config.DefaultWalkParams.MaxRepetitions)
	if batch < 1 {
		batch = 1
	}
	var out []gosnmp.SnmpPDU
	for len(oids) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(len(oids), batch)
		packet, err := snmp.Get(oids[:n])
		if err != nil {
			return nil, err
		}
		if packet == nil {
			return nil, fmt.Errorf("empty SNMP response")
		}
		if packet.Error != gosnmp.NoError {
			return nil, fmt.Errorf("SNMP error status %d", packet.Error)
		}
		out = append(out, packet.Variables...)
		oids = oids[n:]
	}
	return out, nil
}

// FormatRawLine renders one varbind as a single text line: OID, type,
// and value. Strings are quoted. Octet strings that are not text are hex.
// noSuchInstance and the other exceptions have no value.
func FormatRawLine(pdu gosnmp.SnmpPDU) string {
	name := strings.TrimPrefix(pdu.Name, ".")
	if value, ok := rawValue(pdu); ok {
		return name + " " + pdu.Type.String() + " " + value
	}
	return name + " " + pdu.Type.String()
}

func rawValue(pdu gosnmp.SnmpPDU) (string, bool) {
	switch pdu.Type {
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null, gosnmp.EndOfContents:
		return "", false
	}
	switch v := pdu.Value.(type) {
	case nil:
		return "", false
	case string:
		return strconv.Quote(v), true
	case []byte:
		if utf8.Valid(v) && printable(v) {
			return strconv.Quote(string(v)), true
		}
		return "0x" + hex.EncodeToString(v), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case uint:
		return strconv.FormatUint(uint64(v), 10), true
	case uint32:
		return strconv.FormatUint(uint64(v), 10), true
	case uint64:
		return strconv.FormatUint(v, 10), true
	case float32:
		return strconv.FormatFloat(float64(v), 'g', -1, 32), true
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), true
	default:
		return strconv.Quote(fmt.Sprint(v)), true
	}
}

func printable(b []byte) bool {
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if !unicode.IsPrint(r) {
			return false
		}
		b = b[size:]
	}
	return true
}
