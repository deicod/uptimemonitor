package monitor

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// FieldError is a validation failure tied to a specific field. Naming the
// field lets callers (e.g. the IPC layer) map it to a validation_error.field.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// maxHTTPBodyCap is the upper bound for HTTPMonitorConfig.BodyCap (SPEC §11.2.1).
const maxHTTPBodyCap = 16 << 20 // 16 MiB

// hostResolver looks up addresses for hostnames during ICMP validation.
// Tests swap it via swapHostResolver to keep the validator hermetic; the
// runtime path uses net.LookupIP (SPEC §11.2.3).
var hostResolver = func(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// ValidateMonitor checks every monitor field, including the type-specific
// configuration: it decodes Monitor.Config based on Type and dispatches to
// the per-type validator (SPEC §11.2.5). Returns *FieldError naming the
// offending field so the IPC layer can surface validation_error.field.
func ValidateMonitor(m *Monitor) error {
	switch {
	case m.Name == "":
		return &FieldError{"name", "must not be empty"}
	case hasControlChar(m.Name):
		// A name is a display string carried into notification payloads (e.g.
		// the email Subject) and the TUI. Control characters there enable
		// header injection (CWE-93) and break rendering, so reject them at the
		// source rather than trusting every downstream consumer to sanitize.
		return &FieldError{"name", "must not contain control characters"}
	}
	if !isSupportedMonitorType(m.Type) {
		return &FieldError{"type", fmt.Sprintf("unsupported monitor type %q", m.Type)}
	}
	switch {
	case m.Interval <= 0:
		return &FieldError{"interval", "must be positive"}
	case m.Timeout <= 0:
		return &FieldError{"timeout", "must be positive"}
	}
	return validateConfigByType(m)
}

// isSupportedMonitorType reports whether t is one of the v0.2.0 monitor
// types (SPEC §11.2).
func isSupportedMonitorType(t MonitorType) bool {
	switch t {
	case MonitorTypeHTTP, MonitorTypeTCP, MonitorTypePing, MonitorTypeDNS:
		return true
	}
	return false
}

// validateConfigByType decodes m.Config according to m.Type and runs the
// per-type validator. The caller has already established that m.Type is one
// of the supported values.
func validateConfigByType(m *Monitor) error {
	switch m.Type {
	case MonitorTypeHTTP:
		var cfg HTTPMonitorConfig
		if err := json.Unmarshal(m.Config, &cfg); err != nil {
			return &FieldError{"config", "must be a valid HTTP config: " + err.Error()}
		}
		return ValidateHTTPConfig(&cfg)
	case MonitorTypeTCP:
		var cfg TCPMonitorConfig
		if err := json.Unmarshal(m.Config, &cfg); err != nil {
			return &FieldError{"config", "must be a valid TCP config: " + err.Error()}
		}
		return ValidateTCPConfig(&cfg)
	case MonitorTypePing:
		var cfg ICMPPingMonitorConfig
		if err := json.Unmarshal(m.Config, &cfg); err != nil {
			return &FieldError{"config", "must be a valid ICMP ping config: " + err.Error()}
		}
		return ValidateICMPPingConfig(&cfg)
	case MonitorTypeDNS:
		var cfg DNSMonitorConfig
		if err := json.Unmarshal(m.Config, &cfg); err != nil {
			return &FieldError{"config", "must be a valid DNS config: " + err.Error()}
		}
		return ValidateDNSConfig(&cfg)
	}
	// Unreachable: ValidateMonitor gates Type via isSupportedMonitorType.
	return &FieldError{"type", fmt.Sprintf("unsupported monitor type %q", m.Type)}
}

// hasControlChar reports whether s contains any Unicode control character
// (C0/C1, including CR, LF, NUL, and tab). Printable Unicode text — accented
// letters, dashes, emoji — is allowed.
func hasControlChar(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// ValidateHTTPConfig checks an HTTP monitor's type-specific configuration
// against the SPEC §11.2.1 rules.
func ValidateHTTPConfig(c *HTTPMonitorConfig) error {
	u, err := url.Parse(c.URL)
	switch {
	case c.URL == "":
		return &FieldError{"url", "must not be empty"}
	case err != nil:
		return &FieldError{"url", "must be a valid URL"}
	case !u.IsAbs():
		return &FieldError{"url", "must be an absolute URL"}
	case u.Scheme != "http" && u.Scheme != "https":
		return &FieldError{"url", "scheme must be http or https"}
	case u.Host == "":
		return &FieldError{"url", "must include a host"}
	case c.Method != "GET":
		return &FieldError{"method", "must be GET"}
	case c.ExpectedStatusMin < 100 || c.ExpectedStatusMin > 599:
		return &FieldError{"expected_status_min", "must be between 100 and 599"}
	case c.ExpectedStatusMax < 100 || c.ExpectedStatusMax > 599:
		return &FieldError{"expected_status_max", "must be between 100 and 599"}
	case c.ExpectedStatusMin > c.ExpectedStatusMax:
		return &FieldError{"expected_status_max", "must not be less than expected_status_min"}
	}
	// BodyCap=0 means "use the runner default" (SPEC §11.2.1); only enforce
	// bounds on explicit values.
	if c.BodyCap != 0 {
		switch {
		case c.BodyCap < 1024:
			return &FieldError{"body_cap", "must be at least 1024 bytes (or 0 to use the default)"}
		case c.BodyCap > maxHTTPBodyCap:
			return &FieldError{"body_cap", fmt.Sprintf("must be at most %d bytes", maxHTTPBodyCap)}
		}
	}
	if c.Keyword != nil {
		switch c.Keyword.Mode {
		case HTTPKeywordContains, HTTPKeywordNotContains, HTTPKeywordRegex:
		default:
			return &FieldError{"keyword.mode", fmt.Sprintf("unsupported keyword mode %q", c.Keyword.Mode)}
		}
		if c.Keyword.Value == "" {
			return &FieldError{"keyword.value", "must not be empty"}
		}
		// Compiling the regex now means a typo fails on save (where the
		// operator can see it) instead of at the next scheduled probe.
		if c.Keyword.Mode == HTTPKeywordRegex {
			if _, err := regexp.Compile(c.Keyword.Value); err != nil {
				return &FieldError{"keyword.value", "regex does not compile: " + err.Error()}
			}
		}
	}
	return nil
}

// ValidateTCPConfig checks a TCP monitor's type-specific configuration
// against the SPEC §11.2.2 rules.
func ValidateTCPConfig(c *TCPMonitorConfig) error {
	if c.Host == "" {
		return &FieldError{"host", "must not be empty"}
	}
	if net.ParseIP(c.Host) == nil && !isDNSName(c.Host) {
		return &FieldError{"host", "must be a DNS name or textual IP address"}
	}
	if c.Port < 1 || c.Port > 65535 {
		return &FieldError{"port", "must be between 1 and 65535"}
	}
	return nil
}

// ValidateICMPPingConfig checks an ICMP ping monitor's type-specific
// configuration against the SPEC §11.2.3 rules. Hostnames are resolved at
// validation time so IPv6-only setups surface as a config error instead of a
// flapping monitor; tests swap the resolver to stay hermetic.
func ValidateICMPPingConfig(c *ICMPPingMonitorConfig) error {
	if c.Host == "" {
		return &FieldError{"host", "must not be empty"}
	}
	// PacketCount=0 is the SPEC default of 1; only enforce bounds on
	// explicit values (which still cover the [1, 5] range).
	if c.PacketCount < 0 || c.PacketCount > 5 {
		return &FieldError{"packet_count", "must be between 0 and 5"}
	}
	if ip := net.ParseIP(c.Host); ip != nil {
		if ip.To4() == nil {
			return &FieldError{"host", "IPv6 ICMP is deferred; use an IPv4 address or a hostname that resolves to IPv4"}
		}
		return nil
	}
	if !isDNSName(c.Host) {
		return &FieldError{"host", "must be a DNS name or textual IPv4 address"}
	}
	addrs, err := hostResolver(c.Host)
	if err != nil {
		return &FieldError{"host", "DNS lookup failed: " + err.Error()}
	}
	if len(addrs) == 0 {
		return &FieldError{"host", "DNS lookup returned no addresses"}
	}
	for _, a := range addrs {
		if a.To4() != nil {
			return nil
		}
	}
	return &FieldError{"host", "host resolves only to IPv6; ICMP IPv6 is deferred to a later release"}
}

// ValidateDNSConfig checks a DNS monitor's type-specific configuration
// against the SPEC §11.2.4 rules.
func ValidateDNSConfig(c *DNSMonitorConfig) error {
	if c.Name == "" {
		return &FieldError{"name", "must not be empty"}
	}
	if !isDNSName(c.Name) {
		return &FieldError{"name", "must be a valid DNS name"}
	}
	if !isSupportedDNSRecordType(c.RecordType) {
		return &FieldError{"record_type", fmt.Sprintf("unsupported record type %q", c.RecordType)}
	}
	if c.Resolver != "" {
		if err := validateResolver(c.Resolver); err != nil {
			return err
		}
	}
	if c.ExpectedValue != nil {
		if !isSupportedDNSMatchCondition(c.ExpectedValue.Condition) {
			return &FieldError{"expected_value.condition", fmt.Sprintf("unsupported condition %q", c.ExpectedValue.Condition)}
		}
		if c.ExpectedValue.Value == "" {
			return &FieldError{"expected_value.value", "must not be empty"}
		}
	}
	return nil
}

// validateResolver checks that resolver parses as host:port with a port in
// [1, 65535] (SPEC §11.2.4).
func validateResolver(resolver string) error {
	host, portStr, err := net.SplitHostPort(resolver)
	if err != nil {
		return &FieldError{"resolver", "must be host:port"}
	}
	if host == "" {
		return &FieldError{"resolver", "host part must not be empty"}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return &FieldError{"resolver", "port must be a number"}
	}
	if port < 1 || port > 65535 {
		return &FieldError{"resolver", "port must be between 1 and 65535"}
	}
	return nil
}

// isSupportedDNSRecordType reports whether rt is one of the SPEC §11.2.4
// record-type constants.
func isSupportedDNSRecordType(rt DNSRecordType) bool {
	switch rt {
	case DNSRecordA, DNSRecordAAAA, DNSRecordCNAME, DNSRecordMX, DNSRecordTXT, DNSRecordNS:
		return true
	}
	return false
}

// isSupportedDNSMatchCondition reports whether c is one of the eight SPEC
// §11.2.4 match-condition constants.
func isSupportedDNSMatchCondition(c DNSMatchCondition) bool {
	switch c {
	case DNSCondEquals, DNSCondNotEquals,
		DNSCondContains, DNSCondNotContains,
		DNSCondStartsWith, DNSCondNotStartsWith,
		DNSCondEndsWith, DNSCondNotEndsWith:
		return true
	}
	return false
}

// isDNSName reports whether s is a syntactically valid DNS name per
// RFC 1035 / RFC 1123: labels are 1-63 alphanumeric+hyphen bytes (not
// starting or ending with a hyphen) separated by dots, and the whole name
// (excluding any trailing dot) is 1-253 bytes.
func isDNSName(s string) bool {
	if s == "" {
		return false
	}
	s = strings.TrimSuffix(s, ".")
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z':
			case c >= 'A' && c <= 'Z':
			case c >= '0' && c <= '9':
			case c == '-':
				if i == 0 || i == len(label)-1 {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}
