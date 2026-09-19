package tui

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// formMonitorTypes lists the monitor types the form can create, in selector
// order (SPEC §11.2). ICMP ping is not offered: it has no probe runner yet.
var formMonitorTypes = []string{"http", "tcp", "dns"}

// dnsRecordTypes lists the record types a DNS monitor can query (SPEC
// §11.2.4), in selector order.
var dnsRecordTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SOA"}

// dnsConditionNone is the selector entry for "no expected-value check".
const dnsConditionNone = "none"

// dnsConditions lists the expected-value selector entries: none, then the
// eight SPEC §11.2.4 match conditions.
var dnsConditions = []string{
	dnsConditionNone,
	"equals", "not_equals",
	"contains", "not_contains",
	"starts_with", "not_starts_with",
	"ends_with", "not_ends_with",
}

// tcpFormConfig mirrors the SPEC §11.2.2 TCPMonitorConfig JSON shape; like
// httpFormConfig it keeps the TUI independent of the monitor domain package.
type tcpFormConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// dnsFormConfig mirrors the SPEC §11.2.4 DNSMonitorConfig JSON shape.
type dnsFormConfig struct {
	Name          string           `json:"name"`
	RecordType    string           `json:"record_type"`
	Resolver      string           `json:"resolver,omitempty"`
	ExpectedValue *dnsFormExpected `json:"expected_value,omitempty"`
}

// dnsFormExpected mirrors the SPEC §11.2.4 DNSExpectedValue JSON shape.
type dnsFormExpected struct {
	Condition string `json:"condition"`
	Value     string `json:"value"`
}

// httpCheckDetails, tcpCheckDetails, and dnsCheckDetails mirror the fields of
// the SPEC §15.3 check-result Details payloads that the TUI renders.
type httpCheckDetails struct {
	StatusCode *int `json:"status_code"`
}

type tcpCheckDetails struct {
	RemoteAddr string `json:"remote_addr"`
}

type dnsCheckDetails struct {
	RCode   string   `json:"rcode"`
	Records []string `json:"records"`
}

// monitorTarget summarises what a monitor checks — the URL, "host:port", or
// "name TYPE @ resolver" — for the list and detail screens. A config that
// does not decode renders as a dash.
func monitorTarget(m ipc.MonitorResponse) string {
	switch m.Type {
	case "http":
		var c httpFormConfig
		if json.Unmarshal(m.Config, &c) == nil {
			return c.URL
		}
	case "tcp":
		var c tcpFormConfig
		if json.Unmarshal(m.Config, &c) == nil {
			return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
		}
	case "dns":
		var c dnsFormConfig
		if json.Unmarshal(m.Config, &c) == nil {
			resolver := c.Resolver
			if resolver == "" {
				resolver = "system"
			}
			return fmt.Sprintf("%s %s @ %s", c.Name, c.RecordType, resolver)
		}
	}
	return "—"
}

// checkRecordsWidth caps the DNS records shown on one recent-check row.
const checkRecordsWidth = 72

// checkSummary picks the per-type observation shown on a recent-check row
// (SPEC §15.3): status is the short column (HTTP status code, TCP remote
// address, DNS response code) and info the optional trailing text (the DNS
// records). Absent or undecodable Details render status as a dash.
func checkSummary(monitorType string, c ipc.CheckResultResponse) (status, info string) {
	status = "—"
	if len(c.Details) == 0 {
		return status, ""
	}
	switch monitorType {
	case "http":
		var d httpCheckDetails
		if json.Unmarshal(c.Details, &d) == nil && d.StatusCode != nil {
			status = strconv.Itoa(*d.StatusCode)
		}
	case "tcp":
		var d tcpCheckDetails
		if json.Unmarshal(c.Details, &d) == nil && d.RemoteAddr != "" {
			status = d.RemoteAddr
		}
	case "dns":
		var d dnsCheckDetails
		if json.Unmarshal(c.Details, &d) == nil {
			if d.RCode != "" {
				status = d.RCode
			}
			info = truncate(displaySafe(strings.Join(d.Records, ", ")), checkRecordsWidth)
		}
	}
	return status, info
}

// displaySafe replaces control characters with U+FFFD. DNS answers (TXT in
// particular) are remote data and could otherwise smuggle terminal escape
// sequences into the TUI.
func displaySafe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}
