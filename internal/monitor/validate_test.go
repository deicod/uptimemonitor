package monitor

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func validMonitor() *Monitor {
	return &Monitor{
		Name:     "example",
		Type:     MonitorTypeHTTP,
		Interval: 60 * time.Second,
		Timeout:  10 * time.Second,
		Config:   mustJSON(*validHTTPConfig()),
	}
}

// Each invalid case must fail on the named field so the IPC layer can point
// the user at the exact input to correct.
func TestValidateMonitor(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Monitor)
		wantField string // "" means the monitor should be accepted
	}{
		{"valid baseline", func(*Monitor) {}, ""},
		{"empty name", func(m *Monitor) { m.Name = "" }, "name"},
		// Control characters in a name would inject SMTP headers via the email
		// provider's Subject (CWE-93) and corrupt TUI rendering; reject them
		// here rather than relying on each consumer to sanitize.
		{"name with CRLF", func(m *Monitor) { m.Name = "a\r\nBcc: evil@example.com" }, "name"},
		{"name with NUL", func(m *Monitor) { m.Name = "a\x00b" }, "name"},
		{"name with tab", func(m *Monitor) { m.Name = "a\tb" }, "name"},
		{"name with unicode letters accepted", func(m *Monitor) { m.Name = "café—naïve" }, ""},
		{"unsupported type", func(m *Monitor) { m.Type = "smtp" }, "type"},
		{"empty type", func(m *Monitor) { m.Type = "" }, "type"},
		{"zero interval", func(m *Monitor) { m.Interval = 0 }, "interval"},
		{"negative interval", func(m *Monitor) { m.Interval = -time.Second }, "interval"},
		{"zero timeout", func(m *Monitor) { m.Timeout = 0 }, "timeout"},
		{"negative timeout", func(m *Monitor) { m.Timeout = -time.Second }, "timeout"},
		{"missing config", func(m *Monitor) { m.Config = nil }, "config"},
		{"unparsable config", func(m *Monitor) { m.Config = json.RawMessage("not json") }, "config"},
		// Dispatch: the common validator must hand off to the per-type
		// validator, so a bad HTTP url surfaces here too.
		{"http dispatches to http validator", func(m *Monitor) {
			m.Config = mustJSON(HTTPMonitorConfig{URL: "not-absolute", Method: "GET", ExpectedStatusMin: 200, ExpectedStatusMax: 299})
		}, "url"},
		{"tcp valid baseline", func(m *Monitor) {
			m.Type = MonitorTypeTCP
			m.Config = mustJSON(*validTCPConfig())
		}, ""},
		{"tcp dispatches to tcp validator", func(m *Monitor) {
			m.Type = MonitorTypeTCP
			m.Config = mustJSON(TCPMonitorConfig{Host: "example.com", Port: 0})
		}, "port"},
		// Ping has no probe runner yet: even a valid ping config is refused on
		// the type, so no monitor exists that would fail every check.
		{"ping refused until its runner exists", func(m *Monitor) {
			m.Type = MonitorTypePing
			m.Config = mustJSON(*validPingConfig())
		}, "type"},
		{"ping refused before its config is checked", func(m *Monitor) {
			m.Type = MonitorTypePing
			m.Config = mustJSON(ICMPPingMonitorConfig{Host: "", PacketCount: 1})
		}, "type"},
		{"dns valid baseline", func(m *Monitor) {
			m.Type = MonitorTypeDNS
			m.Config = mustJSON(*validDNSConfig())
		}, ""},
		// The monitor name is valid here, so the error must come from the DNS
		// validator — and must say config.name, not the monitor's own name.
		{"dns dispatches to dns validator", func(m *Monitor) {
			m.Type = MonitorTypeDNS
			m.Config = mustJSON(DNSMonitorConfig{Name: "", RecordType: DNSRecordA})
		}, "config.name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validMonitor()
			tc.mutate(m)
			assertField(t, ValidateMonitor(m), tc.wantField)
		})
	}
}

func validHTTPConfig() *HTTPMonitorConfig {
	return &HTTPMonitorConfig{
		URL:               "https://example.com/health",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	}
}

// HTTP config rules from SPEC §11.2.1: absolute URL, http/https scheme, GET
// method, valid expected-status range, BodyCap bounds, and Keyword validation
// (including compile-time regex check).
func TestValidateHTTPConfig(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*HTTPMonitorConfig)
		wantField string
	}{
		{"valid baseline", func(*HTTPMonitorConfig) {}, ""},
		{"empty url", func(c *HTTPMonitorConfig) { c.URL = "" }, "url"},
		{"relative url", func(c *HTTPMonitorConfig) { c.URL = "/health" }, "url"},
		{"non-http scheme", func(c *HTTPMonitorConfig) { c.URL = "ftp://example.com" }, "url"},
		{"missing host", func(c *HTTPMonitorConfig) { c.URL = "http://" }, "url"},
		{"http scheme accepted", func(c *HTTPMonitorConfig) { c.URL = "http://example.com" }, ""},
		{"non-GET method", func(c *HTTPMonitorConfig) { c.Method = "POST" }, "method"},
		{"status min too low", func(c *HTTPMonitorConfig) { c.ExpectedStatusMin = 99 }, "expected_status_min"},
		{"status max too high", func(c *HTTPMonitorConfig) { c.ExpectedStatusMax = 600 }, "expected_status_max"},
		{"min greater than max", func(c *HTTPMonitorConfig) {
			c.ExpectedStatusMin = 300
			c.ExpectedStatusMax = 299
		}, "expected_status_max"},
		// Zero is the sentinel for "use the runner default", so it must be
		// accepted even though it is below the 1024-byte floor that applies
		// to explicit values.
		{"body cap zero accepted", func(c *HTTPMonitorConfig) { c.BodyCap = 0 }, ""},
		{"body cap minimum accepted", func(c *HTTPMonitorConfig) { c.BodyCap = 1024 }, ""},
		{"body cap maximum accepted", func(c *HTTPMonitorConfig) { c.BodyCap = 16 << 20 }, ""},
		{"body cap below minimum", func(c *HTTPMonitorConfig) { c.BodyCap = 1023 }, "body_cap"},
		{"body cap above maximum", func(c *HTTPMonitorConfig) { c.BodyCap = (16 << 20) + 1 }, "body_cap"},
		{"body cap negative", func(c *HTTPMonitorConfig) { c.BodyCap = -1 }, "body_cap"},
		{"keyword contains accepted", func(c *HTTPMonitorConfig) {
			c.Keyword = &HTTPKeyword{Mode: HTTPKeywordContains, Value: "ok"}
		}, ""},
		{"keyword not_contains accepted", func(c *HTTPMonitorConfig) {
			c.Keyword = &HTTPKeyword{Mode: HTTPKeywordNotContains, Value: "error"}
		}, ""},
		{"keyword regex accepted", func(c *HTTPMonitorConfig) {
			c.Keyword = &HTTPKeyword{Mode: HTTPKeywordRegex, Value: "(?i)healthy"}
		}, ""},
		{"keyword unknown mode", func(c *HTTPMonitorConfig) {
			c.Keyword = &HTTPKeyword{Mode: "matches", Value: "ok"}
		}, "keyword.mode"},
		{"keyword empty value", func(c *HTTPMonitorConfig) {
			c.Keyword = &HTTPKeyword{Mode: HTTPKeywordContains, Value: ""}
		}, "keyword.value"},
		// Catching a malformed regex at config-save time is the whole point
		// of the validation-time compile; otherwise a typo would hide until
		// the next scheduled probe.
		{"keyword regex does not compile", func(c *HTTPMonitorConfig) {
			c.Keyword = &HTTPKeyword{Mode: HTTPKeywordRegex, Value: "[invalid"}
		}, "keyword.value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validHTTPConfig()
			tc.mutate(c)
			assertField(t, ValidateHTTPConfig(c), tc.wantField)
		})
	}
}

func validTCPConfig() *TCPMonitorConfig {
	return &TCPMonitorConfig{Host: "example.com", Port: 22}
}

// TCP config rules from SPEC §11.2.2: non-empty host (DNS or textual IP), and
// port in [1, 65535].
func TestValidateTCPConfig(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*TCPMonitorConfig)
		wantField string
	}{
		{"valid baseline", func(*TCPMonitorConfig) {}, ""},
		{"empty host", func(c *TCPMonitorConfig) { c.Host = "" }, "host"},
		{"ipv4 host accepted", func(c *TCPMonitorConfig) { c.Host = "192.0.2.1" }, ""},
		{"ipv6 host accepted", func(c *TCPMonitorConfig) { c.Host = "2001:db8::1" }, ""},
		{"hostname with hyphen accepted", func(c *TCPMonitorConfig) { c.Host = "host-1.example.com" }, ""},
		{"hostname with trailing dot accepted", func(c *TCPMonitorConfig) { c.Host = "example.com." }, ""},
		{"hostname with spaces rejected", func(c *TCPMonitorConfig) { c.Host = "bad host" }, "host"},
		{"hostname starting with hyphen rejected", func(c *TCPMonitorConfig) { c.Host = "-bad.example.com" }, "host"},
		{"hostname underscore rejected", func(c *TCPMonitorConfig) { c.Host = "bad_label.example.com" }, "host"},
		{"hostname too long rejected", func(c *TCPMonitorConfig) { c.Host = strings.Repeat("a", 254) }, "host"},
		// The port has its own field; a host carrying one (or IPv6 brackets,
		// which only make sense next to a port) would be dialled wrongly.
		{"host with port rejected", func(c *TCPMonitorConfig) { c.Host = "example.com:22" }, "host"},
		{"bracketed ipv6 rejected", func(c *TCPMonitorConfig) { c.Host = "[2001:db8::1]" }, "host"},
		{"port zero", func(c *TCPMonitorConfig) { c.Port = 0 }, "port"},
		{"port negative", func(c *TCPMonitorConfig) { c.Port = -1 }, "port"},
		{"port too high", func(c *TCPMonitorConfig) { c.Port = 65536 }, "port"},
		{"port one accepted", func(c *TCPMonitorConfig) { c.Port = 1 }, ""},
		{"port max accepted", func(c *TCPMonitorConfig) { c.Port = 65535 }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validTCPConfig()
			tc.mutate(c)
			assertField(t, ValidateTCPConfig(c), tc.wantField)
		})
	}
}

func validPingConfig() *ICMPPingMonitorConfig {
	// Textual IPv4 skips DNS so tests stay hermetic.
	return &ICMPPingMonitorConfig{Host: "192.0.2.1", PacketCount: 1}
}

// swapHostResolver replaces the package's ICMP-validation resolver for the
// scope of one test. Tests must not call t.Parallel() while a swap is active.
func swapHostResolver(t *testing.T, fn func(host string) ([]net.IP, error)) {
	t.Helper()
	prev := hostResolver
	hostResolver = fn
	t.Cleanup(func() { hostResolver = prev })
}

// ICMP config rules from SPEC §11.2.3: non-empty host, IPv4-only at IP level,
// packet count in [0, 5] (0 means default 1), and rejection of hostnames that
// resolve only to IPv6.
func TestValidateICMPPingConfig(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*ICMPPingMonitorConfig)
		wantField string
	}{
		{"valid ipv4 baseline", func(*ICMPPingMonitorConfig) {}, ""},
		{"empty host", func(c *ICMPPingMonitorConfig) { c.Host = "" }, "host"},
		// IPv6 ICMP is deferred (§15.2.3); a textual IPv6 address must be
		// rejected up-front rather than letting the runner fail at probe
		// time.
		{"textual ipv6 host rejected", func(c *ICMPPingMonitorConfig) { c.Host = "2001:db8::1" }, "host"},
		{"hostname with spaces rejected", func(c *ICMPPingMonitorConfig) { c.Host = "bad host" }, "host"},
		{"packet count zero accepted", func(c *ICMPPingMonitorConfig) { c.PacketCount = 0 }, ""},
		{"packet count one accepted", func(c *ICMPPingMonitorConfig) { c.PacketCount = 1 }, ""},
		{"packet count five accepted", func(c *ICMPPingMonitorConfig) { c.PacketCount = 5 }, ""},
		{"packet count negative", func(c *ICMPPingMonitorConfig) { c.PacketCount = -1 }, "packet_count"},
		{"packet count too high", func(c *ICMPPingMonitorConfig) { c.PacketCount = 6 }, "packet_count"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validPingConfig()
			tc.mutate(c)
			assertField(t, ValidateICMPPingConfig(c), tc.wantField)
		})
	}

	t.Run("hostname resolving to ipv4 accepted", func(t *testing.T) {
		swapHostResolver(t, func(host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.0.2.1")}, nil
		})
		c := validPingConfig()
		c.Host = "ipv4.example.com"
		assertField(t, ValidateICMPPingConfig(c), "")
	})

	t.Run("hostname with mixed ipv4 and ipv6 accepted", func(t *testing.T) {
		swapHostResolver(t, func(host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("192.0.2.1")}, nil
		})
		c := validPingConfig()
		c.Host = "dual.example.com"
		assertField(t, ValidateICMPPingConfig(c), "")
	})

	t.Run("hostname resolving only to ipv6 rejected", func(t *testing.T) {
		swapHostResolver(t, func(host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("2001:db8::1")}, nil
		})
		c := validPingConfig()
		c.Host = "ipv6only.example.com"
		assertField(t, ValidateICMPPingConfig(c), "host")
	})

	t.Run("hostname dns lookup failure rejected", func(t *testing.T) {
		swapHostResolver(t, func(host string) ([]net.IP, error) {
			return nil, errors.New("no such host")
		})
		c := validPingConfig()
		c.Host = "missing.example.com"
		assertField(t, ValidateICMPPingConfig(c), "host")
	})
}

func validDNSConfig() *DNSMonitorConfig {
	return &DNSMonitorConfig{Name: "example.com", RecordType: DNSRecordA}
}

// DNS config rules from SPEC §11.2.4: valid FQDN name, supported record type,
// optional `host:port` resolver, and non-empty ExpectedValue.Value when set.
func TestValidateDNSConfig(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*DNSMonitorConfig)
		wantField string
	}{
		{"valid baseline", func(*DNSMonitorConfig) {}, ""},
		// The query name reports as config.name: a bare "name" would point the
		// operator at the monitor's display name instead.
		{"empty name", func(c *DNSMonitorConfig) { c.Name = "" }, "config.name"},
		{"invalid name", func(c *DNSMonitorConfig) { c.Name = "bad host.example.com" }, "config.name"},
		{"name label too long", func(c *DNSMonitorConfig) { c.Name = strings.Repeat("a", 64) + ".example.com" }, "config.name"},
		{"name with leading hyphen label", func(c *DNSMonitorConfig) { c.Name = "-bad.example.com" }, "config.name"},
		{"name with empty label", func(c *DNSMonitorConfig) { c.Name = "a..example.com" }, "config.name"},
		{"underscore name accepted", func(c *DNSMonitorConfig) { c.Name = "_dmarc.example.com" }, ""},
		{"fqdn trailing dot accepted", func(c *DNSMonitorConfig) { c.Name = "example.com." }, ""},
		{"unsupported record type", func(c *DNSMonitorConfig) { c.RecordType = "SRV" }, "record_type"},
		{"ANY rejected", func(c *DNSMonitorConfig) { c.RecordType = "ANY" }, "record_type"},
		{"AXFR rejected", func(c *DNSMonitorConfig) { c.RecordType = "AXFR" }, "record_type"},
		{"lowercase record type rejected", func(c *DNSMonitorConfig) { c.RecordType = "soa" }, "record_type"},
		{"empty record type", func(c *DNSMonitorConfig) { c.RecordType = "" }, "record_type"},
		{"resolver omitted accepted", func(c *DNSMonitorConfig) { c.Resolver = "" }, ""},
		{"resolver host:port accepted", func(c *DNSMonitorConfig) { c.Resolver = "1.1.1.1:53" }, ""},
		{"resolver ipv6 bracketed accepted", func(c *DNSMonitorConfig) { c.Resolver = "[2001:db8::1]:53" }, ""},
		// A resolver without a port defaults to 53, so an operator can point a
		// monitor at "ns1.example.com" directly.
		{"resolver ipv4 without port accepted", func(c *DNSMonitorConfig) { c.Resolver = "1.1.1.1" }, ""},
		{"resolver hostname without port accepted", func(c *DNSMonitorConfig) { c.Resolver = "ns1.example.com" }, ""},
		{"resolver hostname with port accepted", func(c *DNSMonitorConfig) { c.Resolver = "ns1.example.com:5353" }, ""},
		{"resolver bare ipv6 accepted", func(c *DNSMonitorConfig) { c.Resolver = "2001:db8::1" }, ""},
		{"resolver bracketed ipv6 without port accepted", func(c *DNSMonitorConfig) { c.Resolver = "[2001:db8::1]" }, ""},
		{"resolver brackets around ipv4", func(c *DNSMonitorConfig) { c.Resolver = "[1.1.1.1]" }, "resolver"},
		{"resolver bad hostname", func(c *DNSMonitorConfig) { c.Resolver = "bad host" }, "resolver"},
		{"resolver underscore hostname", func(c *DNSMonitorConfig) { c.Resolver = "_ns.example.com" }, "resolver"},
		{"resolver empty host with port", func(c *DNSMonitorConfig) { c.Resolver = ":53" }, "resolver"},
		{"resolver empty port", func(c *DNSMonitorConfig) { c.Resolver = "1.1.1.1:" }, "resolver"},
		{"resolver unbalanced bracket", func(c *DNSMonitorConfig) { c.Resolver = "[2001:db8::1:53" }, "resolver"},
		{"resolver port not numeric", func(c *DNSMonitorConfig) { c.Resolver = "1.1.1.1:dns" }, "resolver"},
		{"resolver port zero", func(c *DNSMonitorConfig) { c.Resolver = "1.1.1.1:0" }, "resolver"},
		{"resolver port too high", func(c *DNSMonitorConfig) { c.Resolver = "1.1.1.1:70000" }, "resolver"},
		{"expected value empty", func(c *DNSMonitorConfig) {
			c.ExpectedValue = &DNSExpectedValue{Condition: DNSCondEquals, Value: ""}
		}, "expected_value.value"},
		{"expected value unknown condition", func(c *DNSMonitorConfig) {
			c.ExpectedValue = &DNSExpectedValue{Condition: "matches", Value: "1.2.3.4"}
		}, "expected_value.condition"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validDNSConfig()
			tc.mutate(c)
			assertField(t, ValidateDNSConfig(c), tc.wantField)
		})
	}

	// Every supported record type and every supported match condition must
	// be accepted; otherwise the dispatch table and the validator would
	// silently disagree.
	t.Run("all record types accepted", func(t *testing.T) {
		for _, rt := range []DNSRecordType{DNSRecordA, DNSRecordAAAA, DNSRecordCNAME, DNSRecordMX, DNSRecordTXT, DNSRecordNS, DNSRecordSOA} {
			c := validDNSConfig()
			c.RecordType = rt
			if err := ValidateDNSConfig(c); err != nil {
				t.Errorf("record type %q: unexpected error %v", rt, err)
			}
		}
	})

	t.Run("all match conditions accepted", func(t *testing.T) {
		conds := []DNSMatchCondition{
			DNSCondEquals, DNSCondNotEquals,
			DNSCondContains, DNSCondNotContains,
			DNSCondStartsWith, DNSCondNotStartsWith,
			DNSCondEndsWith, DNSCondNotEndsWith,
		}
		if len(conds) != 8 {
			t.Fatalf("expected 8 conditions, have %d", len(conds))
		}
		for _, cond := range conds {
			c := validDNSConfig()
			c.ExpectedValue = &DNSExpectedValue{Condition: cond, Value: "match"}
			if err := ValidateDNSConfig(c); err != nil {
				t.Errorf("condition %q: unexpected error %v", cond, err)
			}
		}
	})
}

// assertField checks that err is nil when wantField is empty, or otherwise a
// *FieldError naming wantField.
func assertField(t *testing.T, err error, wantField string) {
	t.Helper()
	if wantField == "" {
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return
	}
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("expected *FieldError, got %v", err)
	}
	if fe.Field != wantField {
		t.Errorf("error field = %q, want %q", fe.Field, wantField)
	}
}

// TestResolverAddress pins the normalised address the DNS runner dials for
// each accepted resolver spelling: the default port is 53 and IPv6 hosts are
// always bracketed, so the runner never has to re-parse the setting.
func TestResolverAddress(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ns1.example.com", "ns1.example.com:53"},
		{"ns1.example.com.", "ns1.example.com.:53"},
		{"ns1.example.com:5353", "ns1.example.com:5353"},
		{"192.0.2.1", "192.0.2.1:53"},
		{"192.0.2.1:53", "192.0.2.1:53"},
		{"2001:db8::1", "[2001:db8::1]:53"},
		{"[2001:db8::1]", "[2001:db8::1]:53"},
		{"[2001:db8::1]:5353", "[2001:db8::1]:5353"},
	}
	for _, tc := range cases {
		got, err := ResolverAddress(tc.in)
		if err != nil {
			t.Errorf("ResolverAddress(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ResolverAddress(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
