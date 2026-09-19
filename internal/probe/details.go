package probe

import "encoding/json"

// HTTPDetails is the Result.Details payload of the HTTP runner (SPEC §15.3).
// StatusCode is absent when no response was received.
type HTTPDetails struct {
	StatusCode *int `json:"status_code,omitempty"`
}

// TCPDetails is the Result.Details payload of the TCP runner (SPEC §15.3).
// RemoteAddr is the resolved "ip:port" the connection was established to; it
// is absent when no connection was made.
type TCPDetails struct {
	RemoteAddr string `json:"remote_addr,omitempty"`
}

// DNSDetails is the Result.Details payload of the DNS runner (SPEC §15.3).
// Records hold the canonical text form of each answer of the queried type —
// the same strings the expected-value check compares against — capped at
// maxDNSDetailRecords; AnswerCount is the uncapped total.
type DNSDetails struct {
	Name       string `json:"name"`
	RecordType string `json:"record_type"`
	// Resolver is "system" or the configured resolver as "host:port".
	Resolver string `json:"resolver"`
	// Server is the "ip:port" that was queried last; absent when no server
	// could be dialled.
	Server string `json:"server,omitempty"`
	// RCode is the response code mnemonic (NOERROR, NXDOMAIN, SERVFAIL, …);
	// absent when no response arrived.
	RCode       string   `json:"rcode,omitempty"`
	AnswerCount int      `json:"answer_count"`
	Records     []string `json:"records,omitempty"`
}

// maxDNSDetailRecords bounds DNSDetails.Records so a large answer set cannot
// bloat every stored check result (SPEC §15.3).
const maxDNSDetailRecords = 10

// marshalDetails encodes a Details payload. The payload types are plain
// structs of strings, ints, and slices, which cannot fail to marshal; nil is
// returned defensively rather than panicking if that ever changes.
func marshalDetails(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
