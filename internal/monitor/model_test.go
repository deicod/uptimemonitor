package monitor

import (
	"encoding/json"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// State constants are persisted in SQLite and exchanged over IPC, so their
// string values are part of the wire/storage contract and must not drift.
func TestMonitorStateValues(t *testing.T) {
	cases := map[MonitorState]string{
		StateUnknown: "unknown",
		StateUp:      "up",
		StateDown:    "down",
		StatePaused:  "paused",
	}
	for state, want := range cases {
		if string(state) != want {
			t.Errorf("state = %q, want %q", state, want)
		}
	}
}

// MonitorType literals are matched against stored monitor rows and IPC
// payloads, so they are part of the wire/storage contract and must not drift.
func TestMonitorTypeValues(t *testing.T) {
	cases := map[MonitorType]string{
		MonitorTypeHTTP: "http",
		MonitorTypeTCP:  "tcp",
		MonitorTypePing: "ping",
		MonitorTypeDNS:  "dns",
	}
	for typ, want := range cases {
		if string(typ) != want {
			t.Errorf("type = %q, want %q", typ, want)
		}
	}
}

// HTTPKeywordMode literals are stored inside monitor config JSON, so the
// constants pin the on-disk and wire shape.
func TestHTTPKeywordModeValues(t *testing.T) {
	cases := map[HTTPKeywordMode]string{
		HTTPKeywordContains:    "contains",
		HTTPKeywordNotContains: "not_contains",
		HTTPKeywordRegex:       "regex",
	}
	for mode, want := range cases {
		if string(mode) != want {
			t.Errorf("mode = %q, want %q", mode, want)
		}
	}
}

// DNSRecordType literals are written into monitor config JSON and queried
// against DNS in §15.2.4, so the constants are part of the contract.
func TestDNSRecordTypeValues(t *testing.T) {
	cases := map[DNSRecordType]string{
		DNSRecordA:     "A",
		DNSRecordAAAA:  "AAAA",
		DNSRecordCNAME: "CNAME",
		DNSRecordMX:    "MX",
		DNSRecordTXT:   "TXT",
		DNSRecordNS:    "NS",
	}
	for rt, want := range cases {
		if string(rt) != want {
			t.Errorf("record type = %q, want %q", rt, want)
		}
	}
}

// All eight DNS match conditions are persisted inside monitor config JSON
// (SPEC §11.2.4) and dispatched against record strings at run time
// (§15.2.4); the literals are a stable contract.
func TestDNSMatchConditionValues(t *testing.T) {
	cases := map[DNSMatchCondition]string{
		DNSCondEquals:        "equals",
		DNSCondNotEquals:     "not_equals",
		DNSCondContains:      "contains",
		DNSCondNotContains:   "not_contains",
		DNSCondStartsWith:    "starts_with",
		DNSCondNotStartsWith: "not_starts_with",
		DNSCondEndsWith:      "ends_with",
		DNSCondNotEndsWith:   "not_ends_with",
	}
	if len(cases) != 8 {
		t.Fatalf("expected 8 DNS match conditions, have %d", len(cases))
	}
	for cond, want := range cases {
		if string(cond) != want {
			t.Errorf("condition = %q, want %q", cond, want)
		}
	}
}

// HTTPMonitorConfig is the JSON payload stored in monitors.config_json and
// sent over IPC; the full shape (including the new BodyCap + Keyword fields
// from v0.2.0) must survive a round-trip without losing data.
func TestHTTPMonitorConfigJSONRoundTrip(t *testing.T) {
	cfg := HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
		BodyCap:           1 << 20,
		Keyword: &HTTPKeyword{
			Mode:  HTTPKeywordRegex,
			Value: "(?i)healthy",
		},
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got HTTPMonitorConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("round-trip mismatch:\n got=%#v\nwant=%#v", got, cfg)
	}
}

// v0.1.0 HTTP monitors store config without body_cap or keyword. The new
// optional fields must be absent from the JSON when unset so existing rows
// and IPC payloads keep their shape (avoiding a no-op migration).
func TestHTTPMonitorConfigOmitsOptionals(t *testing.T) {
	cfg := HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["body_cap"]; ok {
		t.Errorf("body_cap present in JSON; want absent (zero is sentinel for default)")
	}
	if _, ok := m["keyword"]; ok {
		t.Errorf("keyword present in JSON; want absent when nil")
	}
}

func TestTCPMonitorConfigJSONRoundTrip(t *testing.T) {
	cfg := TCPMonitorConfig{Host: "example.com", Port: 22}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got TCPMonitorConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != cfg {
		t.Errorf("round-trip mismatch: got=%#v want=%#v", got, cfg)
	}
}

// PacketCount=0 means "use the default 1" per SPEC §11.2.3, so it must not
// appear on the wire.
func TestICMPPingMonitorConfigJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		cfg  ICMPPingMonitorConfig
	}{
		{"explicit packet count", ICMPPingMonitorConfig{Host: "example.com", PacketCount: 3}},
		{"default packet count", ICMPPingMonitorConfig{Host: "example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.cfg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got ICMPPingMonitorConfig
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got != tt.cfg {
				t.Errorf("round-trip mismatch: got=%#v want=%#v", got, tt.cfg)
			}
		})
	}
}

func TestICMPPingMonitorConfigOmitsPacketCountZero(t *testing.T) {
	cfg := ICMPPingMonitorConfig{Host: "example.com"}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["packet_count"]; ok {
		t.Errorf("packet_count present in JSON; want absent (zero is sentinel for default)")
	}
}

func TestDNSExpectedValueJSONRoundTrip(t *testing.T) {
	ev := DNSExpectedValue{Condition: DNSCondContains, Value: "example."}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got DNSExpectedValue
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != ev {
		t.Errorf("round-trip mismatch: got=%#v want=%#v", got, ev)
	}
}

func TestDNSMonitorConfigJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		cfg  DNSMonitorConfig
	}{
		{
			"custom resolver with expected value",
			DNSMonitorConfig{
				Name:       "example.com",
				RecordType: DNSRecordA,
				Resolver:   "1.1.1.1:53",
				ExpectedValue: &DNSExpectedValue{
					Condition: DNSCondEquals,
					Value:     "93.184.216.34",
				},
			},
		},
		{
			"system resolver, no expected value",
			DNSMonitorConfig{
				Name:       "example.com",
				RecordType: DNSRecordAAAA,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.cfg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got DNSMonitorConfig
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tt.cfg) {
				t.Errorf("round-trip mismatch:\n got=%#v\nwant=%#v", got, tt.cfg)
			}
		})
	}
}

func TestDNSMonitorConfigOmitsOptionals(t *testing.T) {
	cfg := DNSMonitorConfig{Name: "example.com", RecordType: DNSRecordA}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["resolver"]; ok {
		t.Errorf("resolver present in JSON; want absent when empty")
	}
	if _, ok := m["expected_value"]; ok {
		t.Errorf("expected_value present in JSON; want absent when nil")
	}
}

// NewID must yield unique IDs even under concurrent use, since records across
// goroutines (e.g. scheduler workers) rely on IDs as primary keys.
func TestNewIDUnique(t *testing.T) {
	const n = 1000
	var mu sync.Mutex
	seen := make(map[string]struct{}, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			id := NewID()
			mu.Lock()
			seen[id] = struct{}{}
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(seen) != n {
		t.Errorf("got %d unique IDs, want %d", len(seen), n)
	}
}

// IDs must sort lexically into creation order so that ordering rows by ID is
// equivalent to ordering them chronologically (SPEC §6 decision 1).
func TestNewIDLexicallySortable(t *testing.T) {
	const n = 100
	ids := make([]string, n)
	for i := range ids {
		ids[i] = NewID()
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("IDs are not in lexical creation order at index %d", i)
		}
	}
}
