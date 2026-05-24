package email

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/notify"
)

func TestProviderImplementsInterface(t *testing.T) {
	var _ notify.Provider = New()
}

// TestKindAndFields pins the SPEC §18.4/§18.5 field contract: host/port/from/to
// are required, password is a secret, starttls is a bool defaulting to true.
func TestKindAndFields(t *testing.T) {
	p := New()
	if p.Kind() != "email" {
		t.Errorf("Kind() = %q, want email", p.Kind())
	}
	f := p.Fields()
	byName := map[string]notify.Field{}
	for _, fl := range f {
		byName[fl.Name] = fl
	}
	for _, name := range []string{"host", "port", "username", "password", "from", "to", "starttls"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("Fields() missing %q", name)
		}
	}
	if h := byName["host"]; !h.Required {
		t.Errorf("host field = %+v, want required", h)
	}
	if pw := byName["password"]; pw.Type != notify.FieldTypeSecretString || !pw.Secret {
		t.Errorf("password field = %+v, want secret", pw)
	}
	if pt := byName["port"]; pt.Type != notify.FieldTypeNumber || !pt.Required {
		t.Errorf("port field = %+v, want required number", pt)
	}
	if st := byName["starttls"]; st.Type != notify.FieldTypeBool || st.Default != "true" {
		t.Errorf("starttls field = %+v, want bool defaulting true", st)
	}
}

func TestValidate(t *testing.T) {
	const ok = `{"host":"smtp.example.com","port":587,"from":"a@example.com","to":"b@example.com"}`
	cases := []struct{ name, config, wantField string }{
		{"valid", ok, ""},
		{"missing host", `{"port":587,"from":"a@x","to":"b@x"}`, "host"},
		{"missing from", `{"host":"smtp.x","port":587,"to":"b@x"}`, "from"},
		{"missing to", `{"host":"smtp.x","port":587,"from":"a@x"}`, "to"},
		{"port zero", `{"host":"smtp.x","port":0,"from":"a@x","to":"b@x"}`, "port"},
		{"port too high", `{"host":"smtp.x","port":70000,"from":"a@x","to":"b@x"}`, "port"},
		{"empty config", ``, "host"},
	}
	p := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(context.Background(), json.RawMessage(tc.config))
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			var fe *notify.FieldError
			if !errors.As(err, &fe) || fe.Field != tc.wantField {
				t.Fatalf("Validate = %v, want *notify.FieldError(%s)", err, tc.wantField)
			}
		})
	}
}

func TestValidateMalformedJSON(t *testing.T) {
	if err := New().Validate(context.Background(), json.RawMessage(`{bad`)); err == nil {
		t.Fatal("Validate(malformed json) = nil, want error")
	}
}

// TestSendPlaintextWithAuth verifies the core delivery contract over a plaintext
// connection: the provider authenticates with PLAIN, sets the SMTP envelope
// (MAIL FROM / RCPT TO) from config, and writes a message carrying the subject
// and body. AUTH PLAIN over an unencrypted connection is permitted only because
// the test server listens on loopback (net/smtp's localhost exception).
func TestSendPlaintextWithAuth(t *testing.T) {
	srv := newSMTPServer(t, nil)
	host, port := srv.hostPort()
	cfg := fmt.Sprintf(
		`{"host":%q,"port":%d,"username":"alerts","password":"s3cret","from":"alerts@example.com","to":"admin@example.com","starttls":false}`,
		host, port,
	)
	msg := notify.Message{Title: "Monitor down: Example", Body: "It went down.", Time: time.Unix(1700000000, 0)}
	if err := New().Send(context.Background(), json.RawMessage(cfg), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mails := srv.mails()
	if len(mails) != 1 {
		t.Fatalf("server received %d mails, want 1", len(mails))
	}
	m := mails[0]
	if m.from != "alerts@example.com" {
		t.Errorf("MAIL FROM = %q, want alerts@example.com", m.from)
	}
	if m.to != "admin@example.com" {
		t.Errorf("RCPT TO = %q, want admin@example.com", m.to)
	}
	if !m.auth {
		t.Fatal("server did not see AUTH")
	}
	user, pass := decodePlainAuth(t, m.authPayload)
	if user != "alerts" || pass != "s3cret" {
		t.Errorf("auth = (%q,%q), want (alerts,s3cret)", user, pass)
	}
	if !strings.Contains(m.data, "Subject: Monitor down: Example") {
		t.Errorf("data missing subject:\n%s", m.data)
	}
	if !strings.Contains(m.data, "It went down.") {
		t.Errorf("data missing body:\n%s", m.data)
	}
}

// TestSendStartTLS proves that when starttls is enabled the provider upgrades
// the connection before transmitting the envelope and message, so credentials
// and content are not sent in the clear (SPEC §18.5).
func TestSendStartTLS(t *testing.T) {
	srv := newSMTPServer(t, testTLSConfig(t))
	host, port := srv.hostPort()
	cfg := fmt.Sprintf(
		`{"host":%q,"port":%d,"username":"alerts","password":"s3cret","from":"a@example.com","to":"b@example.com","starttls":true}`,
		host, port,
	)
	p := New()
	p.tlsConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server uses a self-signed cert
	if err := p.Send(context.Background(), json.RawMessage(cfg), notify.Message{Title: "x", Body: "y"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mails := srv.mails()
	if len(mails) != 1 {
		t.Fatalf("server received %d mails, want 1", len(mails))
	}
	if !mails[0].tls {
		t.Error("message was delivered before STARTTLS upgrade")
	}
}

// TestSendSubjectHeaderInjection proves that CR/LF in the title (which carries
// the monitor name, validated only for non-emptiness upstream) cannot inject a
// new SMTP header. A naive Subject: %s would let "x\r\nBcc: evil@example.com"
// add a Bcc header (CWE-93); the provider strips CR/LF so the value stays on
// the Subject line.
func TestSendSubjectHeaderInjection(t *testing.T) {
	srv := newSMTPServer(t, nil)
	host, port := srv.hostPort()
	cfg := fmt.Sprintf(
		`{"host":%q,"port":%d,"from":"a@example.com","to":"b@example.com","starttls":false}`,
		host, port,
	)
	title := "Monitor down: x\r\nBcc: evil@example.com"
	if err := New().Send(context.Background(), json.RawMessage(cfg), notify.Message{Title: title}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mails := srv.mails()
	if len(mails) != 1 {
		t.Fatalf("server received %d mails, want 1", len(mails))
	}
	for line := range strings.SplitSeq(mails[0].data, "\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("injected header reached the server:\n%s", mails[0].data)
		}
	}
	if !strings.Contains(mails[0].data, "Subject: Monitor down: xBcc: evil@example.com") {
		t.Errorf("subject not collapsed onto one line:\n%s", mails[0].data)
	}
}

func TestSendDialError(t *testing.T) {
	// Port 1 on loopback refuses connections; Send must surface an error rather
	// than reporting success.
	cfg := `{"host":"127.0.0.1","port":1,"from":"a@x","to":"b@x","starttls":false}`
	if err := New().Send(context.Background(), json.RawMessage(cfg), notify.Message{Title: "x"}); err == nil {
		t.Fatal("Send to a closed port returned nil")
	}
}

func decodePlainAuth(t *testing.T, payload string) (user, pass string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("auth payload not base64: %v", err)
	}
	// PLAIN is identity\x00username\x00password.
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		t.Fatalf("auth payload = %q, want 3 NUL-separated fields", raw)
	}
	return parts[1], parts[2]
}

// --- in-process SMTP test server ---

type receivedMail struct {
	from, to, data string
	auth           bool
	authPayload    string
	tls            bool
}

type smtpServer struct {
	ln     net.Listener
	tlsCfg *tls.Config
	mu     chan struct{} // 1-slot channel used as a mutex
	got    []receivedMail
}

func newSMTPServer(t *testing.T, tlsCfg *tls.Config) *smtpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &smtpServer{ln: ln, tlsCfg: tlsCfg, mu: make(chan struct{}, 1)}
	s.mu <- struct{}{}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.handle(conn)
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *smtpServer) hostPort() (string, int) {
	host, portStr, _ := net.SplitHostPort(s.ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func (s *smtpServer) record(m receivedMail) {
	<-s.mu
	s.got = append(s.got, m)
	s.mu <- struct{}{}
}

func (s *smtpServer) mails() []receivedMail {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	return append([]receivedMail(nil), s.got...)
}

func (s *smtpServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(line string) {
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}
	write("220 test ESMTP")

	var mail receivedMail
	tlsActive := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
			write("250-localhost")
			if s.tlsCfg != nil && !tlsActive {
				write("250-STARTTLS")
			}
			write("250 AUTH PLAIN")
		case strings.HasPrefix(verb, "STARTTLS"):
			write("220 Ready to start TLS")
			tconn := tls.Server(conn, s.tlsCfg)
			if err := tconn.Handshake(); err != nil {
				return
			}
			conn = tconn
			r = bufio.NewReader(conn)
			w = bufio.NewWriter(conn)
			write = func(line string) {
				_, _ = w.WriteString(line + "\r\n")
				_ = w.Flush()
			}
			tlsActive = true
		case strings.HasPrefix(verb, "AUTH"):
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				mail.auth = true
				mail.authPayload = fields[2]
			}
			write("235 2.7.0 Authentication successful")
		case strings.HasPrefix(verb, "MAIL"):
			mail.from = parseAddr(line)
			write("250 OK")
		case strings.HasPrefix(verb, "RCPT"):
			mail.to = parseAddr(line)
			write("250 OK")
		case strings.HasPrefix(verb, "DATA"):
			write("354 End data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				dl = strings.TrimRight(dl, "\r\n")
				if dl == "." {
					break
				}
				if strings.HasPrefix(dl, "..") {
					dl = dl[1:]
				}
				data.WriteString(dl)
				data.WriteByte('\n')
			}
			mail.data = data.String()
			mail.tls = tlsActive
			s.record(mail)
			write("250 OK")
		case strings.HasPrefix(verb, "QUIT"):
			write("221 Bye")
			return
		case strings.HasPrefix(verb, "RSET"), strings.HasPrefix(verb, "NOOP"):
			write("250 OK")
		default:
			write("500 unknown command")
		}
	}
}

func parseAddr(line string) string {
	i := strings.Index(line, "<")
	j := strings.Index(line, ">")
	if i >= 0 && j > i {
		return line[i+1 : j]
	}
	return ""
}

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}}}
}
