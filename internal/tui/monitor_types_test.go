package tui

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// visibleKeys lists the keys of the fields the form currently shows.
func visibleKeys(s *monitorFormScreen) []string {
	keys := make([]string, 0, len(s.fields))
	for _, f := range s.fields {
		keys = append(keys, f.key)
	}
	return keys
}

// focus moves the form cursor onto the visible field key.
func focus(t *testing.T, s *monitorFormScreen, key string) {
	t.Helper()
	i := slices.Index(visibleKeys(s), key)
	if i < 0 {
		t.Fatalf("field %q not visible; visible = %v", key, visibleKeys(s))
	}
	s.cursor = i
	s.syncFocus()
}

// selectType cycles the create form's type selector to typ with the same
// keys an operator would press.
func selectType(t *testing.T, s *monitorFormScreen, typ string) {
	t.Helper()
	focus(t, s, "type")
	for range formMonitorTypes {
		if s.monitorType == typ {
			return
		}
		s.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	t.Fatalf("type selector never reached %q", typ)
}

// submitForm presses ctrl+s and runs the resulting command.
func submitForm(t *testing.T, s *monitorFormScreen) tea.Msg {
	t.Helper()
	_, cmd := s.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("submit produced no command; field errors: %v", fieldErrors(s))
	}
	return cmd()
}

// fieldErrors collects the per-field errors for failure messages.
func fieldErrors(s *monitorFormScreen) map[string]string {
	errs := map[string]string{}
	for _, f := range s.all {
		if f.fieldErr != "" {
			errs[f.key] = f.fieldErr
		}
	}
	return errs
}

// jsonObject decodes raw into a generic map for exact-shape comparisons.
func jsonObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return m
}

// TestMonitorFormTypeSelectorShowsTypeFields checks each type shows its own
// config fields and none of the others', with the cursor staying on the
// selector as the form re-lays out.
func TestMonitorFormTypeSelectorShowsTypeFields(t *testing.T) {
	s := newMonitorFormScreen(&formClient{}, "")
	s.Init()
	want := map[string][]string{
		"http": {"url", "method", "expected_status_min", "expected_status_max"},
		"tcp":  {"host", "port"},
		"dns":  {"config.name", "record_type", "resolver", "expected_value.condition"},
	}
	for _, typ := range []string{"http", "tcp", "dns", "tcp"} {
		selectType(t, s, typ)
		keys := visibleKeys(s)
		for other, fields := range want {
			for _, k := range fields {
				if shown := slices.Contains(keys, k); shown != (other == typ) {
					t.Errorf("type %s: field %q shown = %v; visible = %v", typ, k, shown, keys)
				}
			}
		}
		if s.fields[s.cursor].key != "type" {
			t.Errorf("type %s: cursor on %q, want it to stay on the type selector", typ, s.fields[s.cursor].key)
		}
	}
	// ← steps back through the options.
	s.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if s.monitorType != "http" {
		t.Errorf("left from tcp selected %q, want http", s.monitorType)
	}
}

// TestMonitorFormCreatesTCPMonitor submits the "ns1 SSH" monitor and checks
// the request carries exactly the TCP config — no leftovers from the HTTP
// fields the form started with.
func TestMonitorFormCreatesTCPMonitor(t *testing.T) {
	fc := &formClient{}
	s := newMonitorFormScreen(fc, "")
	s.Init()
	s.setText("url", "https://typed-before-switching.example")
	selectType(t, s, "tcp")
	s.setText("name", "ns1 SSH")
	s.setText("host", "ns1.dysv.de")
	s.setText("port", "22")

	if msg := submitForm(t, s); msg != (monitorSavedMsg{}) {
		t.Fatalf("submit result = %#v, want monitorSavedMsg", msg)
	}
	req := fc.created
	if req == nil || req.Type != "tcp" || req.Name != "ns1 SSH" {
		t.Fatalf("created = %+v, want tcp monitor ns1 SSH", req)
	}
	if got, want := jsonObject(t, req.Config), map[string]any{"host": "ns1.dysv.de", "port": float64(22)}; !reflect.DeepEqual(got, want) {
		t.Errorf("config = %v, want %v", got, want)
	}
	if time.Duration(req.Interval) != time.Minute || time.Duration(req.Timeout) != 10*time.Second {
		t.Errorf("interval/timeout = %v/%v, want the form defaults", time.Duration(req.Interval), time.Duration(req.Timeout))
	}
}

// TestMonitorFormCreatesDNSMonitor submits the "ns1 DNS authoritative" SOA
// monitor, first without and then with an expected-value check.
func TestMonitorFormCreatesDNSMonitor(t *testing.T) {
	fc := &formClient{}
	s := newMonitorFormScreen(fc, "")
	s.Init()
	selectType(t, s, "dns")
	s.setText("name", "ns1 DNS authoritative")
	s.setText("config.name", "dysv.de")
	s.setChoice("record_type", "SOA")
	s.setText("resolver", "ns1.dysv.de")
	s.setText("timeout", "5s")

	submitForm(t, s)
	want := map[string]any{"name": "dysv.de", "record_type": "SOA", "resolver": "ns1.dysv.de"}
	if got := jsonObject(t, fc.created.Config); fc.created.Type != "dns" || !reflect.DeepEqual(got, want) {
		t.Fatalf("created type %q config %v, want dns %v", fc.created.Type, got, want)
	}

	// Choosing a condition reveals the value field, which then becomes part
	// of the config, sent as typed.
	if slices.Contains(visibleKeys(s), "expected_value.value") {
		t.Fatal("expected value shown before a condition was chosen")
	}
	focus(t, s, "expected_value.condition")
	s.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	s.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := s.choiceVal("expected_value.condition"); got != "not_equals" {
		t.Fatalf("condition = %q after two steps, want not_equals", got)
	}
	s.setText("expected_value.value", "ns9.dysv.de. ")
	submitForm(t, s)
	want["expected_value"] = map[string]any{"condition": "not_equals", "value": "ns9.dysv.de. "}
	if got := jsonObject(t, fc.created.Config); !reflect.DeepEqual(got, want) {
		t.Errorf("config = %v, want %v", got, want)
	}
}

// TestMonitorFormTypeSpecificClientValidation checks the form catches what
// it can see before sending anything.
func TestMonitorFormTypeSpecificClientValidation(t *testing.T) {
	for _, tc := range []struct {
		typ     string
		fill    map[string]string
		cond    string
		wantErr []string
	}{
		{"tcp", map[string]string{"host": "", "port": "ssh"}, "", []string{"host", "port"}},
		{"dns", map[string]string{"config.name": ""}, "", []string{"config.name"}},
		{"dns", map[string]string{"config.name": "dysv.de", "expected_value.value": ""}, "equals", []string{"expected_value.value"}},
	} {
		fc := &formClient{}
		s := newMonitorFormScreen(fc, "")
		s.Init()
		selectType(t, s, tc.typ)
		s.setText("name", "x")
		for k, v := range tc.fill {
			s.setText(k, v)
		}
		if tc.cond != "" {
			s.setChoice("expected_value.condition", tc.cond)
			s.relayout()
		}
		if _, cmd := s.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}); cmd != nil || fc.created != nil {
			t.Errorf("%s %v: submit sent a request, want it blocked", tc.typ, tc.fill)
		}
		for _, k := range tc.wantErr {
			if s.field(k).fieldErr == "" {
				t.Errorf("%s %v: no error on %q; errors = %v", tc.typ, tc.fill, k, fieldErrors(s))
			}
		}
	}
}

// TestMonitorFormEditsDNSMonitor loads a stored DNS monitor, checks every
// field is populated, and that saving unchanged sends the same config back.
func TestMonitorFormEditsDNSMonitor(t *testing.T) {
	stored := json.RawMessage(`{"name":"dysv.de","record_type":"SOA","resolver":"ns1.dysv.de",` +
		`"expected_value":{"condition":"starts_with","value":"ns1.dysv.de. "}}`)
	fc := &formClient{}
	s := newMonitorFormScreen(fc, "01DNS")
	scr, _ := s.Update(monitorFormLoadedMsg{monitor: ipc.MonitorResponse{
		ID: "01DNS", Name: "ns1 DNS authoritative", Type: "dns",
		Interval: ipc.Duration(time.Minute), Timeout: ipc.Duration(5 * time.Second), Config: stored,
	}})
	fs := scr.(*monitorFormScreen)

	for key, want := range map[string]string{"config.name": "dysv.de", "resolver": "ns1.dysv.de", "expected_value.value": "ns1.dysv.de. ", "timeout": "5s"} {
		if got := fs.text(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if fs.choiceVal("record_type") != "SOA" || fs.choiceVal("expected_value.condition") != "starts_with" {
		t.Errorf("record type/condition = %s/%s, want SOA/starts_with", fs.choiceVal("record_type"), fs.choiceVal("expected_value.condition"))
	}
	keys := visibleKeys(fs)
	if slices.Contains(keys, "type") || slices.Contains(keys, "url") || !slices.Contains(keys, "expected_value.value") {
		t.Errorf("edit form fields = %v, want the DNS fields without a type selector", keys)
	}
	if !strings.Contains(fs.View(), "type dns cannot be changed") {
		t.Errorf("edit view does not explain the fixed type:\n%s", fs.View())
	}

	submitForm(t, fs)
	if fc.updated == nil || !reflect.DeepEqual(jsonObject(t, fc.updated.Config), jsonObject(t, stored)) {
		t.Fatalf("update config = %s, want the stored config %s", fc.updated.Config, stored)
	}

	// Clearing the condition removes the expected value from the config.
	fs.setChoice("expected_value.condition", dnsConditionNone)
	fs.relayout()
	submitForm(t, fs)
	if _, has := jsonObject(t, fc.updated.Config)["expected_value"]; has {
		t.Errorf("config still has expected_value after clearing the condition: %s", fc.updated.Config)
	}
}

// TestMonitorFormEditsTCPMonitor checks a stored TCP monitor loads into the
// host/port fields and round-trips.
func TestMonitorFormEditsTCPMonitor(t *testing.T) {
	fc := &formClient{}
	s := newMonitorFormScreen(fc, "01TCP")
	scr, _ := s.Update(monitorFormLoadedMsg{monitor: ipc.MonitorResponse{
		ID: "01TCP", Name: "ns1 DNS TCP", Type: "tcp", Interval: ipc.Duration(time.Minute),
		Timeout: ipc.Duration(5 * time.Second), Config: json.RawMessage(`{"host":"2001:db8::53","port":53}`),
	}})
	fs := scr.(*monitorFormScreen)
	if fs.text("host") != "2001:db8::53" || fs.text("port") != "53" {
		t.Fatalf("host/port = %q/%q, want 2001:db8::53/53", fs.text("host"), fs.text("port"))
	}
	fs.setText("port", "5353")
	submitForm(t, fs)
	if got := jsonObject(t, fc.updated.Config); !reflect.DeepEqual(got, map[string]any{"host": "2001:db8::53", "port": float64(5353)}) {
		t.Errorf("update config = %v", got)
	}
}

// TestMonitorFormEditOfUnknownTypeKeepsConfig checks editing a monitor type
// the form has no fields for (ICMP ping) sends no config, so the stored one
// is kept rather than replaced by another type's config.
func TestMonitorFormEditOfUnknownTypeKeepsConfig(t *testing.T) {
	fc := &formClient{}
	s := newMonitorFormScreen(fc, "01PING")
	scr, _ := s.Update(monitorFormLoadedMsg{monitor: ipc.MonitorResponse{
		ID: "01PING", Name: "router", Type: "ping", Interval: ipc.Duration(time.Minute),
		Timeout: ipc.Duration(5 * time.Second), Config: json.RawMessage(`{"host":"192.0.2.1"}`),
	}})
	fs := scr.(*monitorFormScreen)
	fs.setText("name", "router 2")
	submitForm(t, fs)
	if fc.updated == nil || fc.updated.Config != nil || *fc.updated.Name != "router 2" {
		t.Errorf("update = %+v, want the new name and no config", fc.updated)
	}
}

// TestMonitorFormServerErrorsLandOnTypeFields checks server validation
// errors for TCP and DNS config fields are shown on the matching input —
// including the DNS query name, reported as config.name so it cannot be
// confused with the monitor's name.
func TestMonitorFormServerErrorsLandOnTypeFields(t *testing.T) {
	for _, tc := range []struct{ typ, field string }{
		{"tcp", "port"},
		{"tcp", "host"},
		{"dns", "config.name"},
		{"dns", "record_type"},
		{"dns", "resolver"},
	} {
		s := newMonitorFormScreen(&formClient{}, "")
		s.Init()
		selectType(t, s, tc.typ)
		s.Update(monitorFormErrorMsg{apiErr: &ipc.APIError{Code: ipc.ErrValidation, Message: "bad value", Field: tc.field}})
		if got := s.field(tc.field).fieldErr; got != "bad value" {
			t.Errorf("%s/%s: field error = %q, want it on the field", tc.typ, tc.field, got)
		}
		if s.field("name").fieldErr != "" || s.formErr != "" {
			t.Errorf("%s/%s: error also shown on name/form: %q/%q", tc.typ, tc.field, s.field("name").fieldErr, s.formErr)
		}
		if s.fields[s.cursor].key != tc.field {
			t.Errorf("%s/%s: cursor on %q, want the errored field", tc.typ, tc.field, s.fields[s.cursor].key)
		}
	}
}

// TestMonitorTarget pins the one-line summaries the list and detail screens
// show for each type.
func TestMonitorTarget(t *testing.T) {
	for _, tc := range []struct {
		typ, cfg, want string
	}{
		{"http", `{"url":"https://example.com/health"}`, "https://example.com/health"},
		{"tcp", `{"host":"ns1.dysv.de","port":53}`, "ns1.dysv.de:53"},
		{"tcp", `{"host":"2001:db8::53","port":22}`, "[2001:db8::53]:22"},
		{"dns", `{"name":"dysv.de","record_type":"SOA","resolver":"ns1.dysv.de"}`, "dysv.de SOA @ ns1.dysv.de"},
		{"dns", `{"name":"dysv.de","record_type":"A"}`, "dysv.de A @ system"},
		{"ping", `{"host":"192.0.2.1"}`, "—"},
		{"tcp", `not json`, "—"},
	} {
		if got := monitorTarget(ipc.MonitorResponse{Type: tc.typ, Config: json.RawMessage(tc.cfg)}); got != tc.want {
			t.Errorf("monitorTarget(%s %s) = %q, want %q", tc.typ, tc.cfg, got, tc.want)
		}
	}
}

// TestRenderChecksPerType checks the recent-check rows show each type's own
// observation instead of assuming an HTTP status, and that remote DNS data
// cannot inject terminal control sequences.
func TestRenderChecksPerType(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	row := func(state, details, errText string) []ipc.CheckResultResponse {
		return []ipc.CheckResultResponse{{StartedAt: now, State: state, DurationMs: 12, Error: errText, Details: json.RawMessage(details)}}
	}
	for _, tc := range []struct {
		name, typ string
		checks    []ipc.CheckResultResponse
		want      []string
		notWant   []string
	}{
		{"http", "http", row("up", `{"status_code":204}`, ""), []string{"up", "204", "12ms"}, nil},
		{"tcp", "tcp", row("up", `{"remote_addr":"192.0.2.53:22"}`, ""), []string{"192.0.2.53:22"}, nil},
		{"tcp refused", "tcp", row("down", `{}`, "connect: connection refused"), []string{"down", "—", "connect: connection refused"}, nil},
		{"dns", "dns", row("up", `{"rcode":"NOERROR","answer_count":2,"records":["ns1.dysv.de.","ns2.dysv.de."]}`, ""),
			[]string{"NOERROR", "ns1.dysv.de., ns2.dysv.de."}, nil},
		{"dns nxdomain", "dns", row("down", `{"rcode":"NXDOMAIN","answer_count":0}`, "response code NXDOMAIN"),
			[]string{"NXDOMAIN", "response code NXDOMAIN"}, nil},
		{"dns hostile txt", "dns", row("up", `{"rcode":"NOERROR","records":["a\u001b[2Jb"]}`, ""),
			[]string{"a�[2Jb"}, []string{"\x1b"}},
		{"no details", "dns", row("down", ``, "probe configuration error"), []string{"—", "probe configuration error"}, nil},
	} {
		out := renderChecks(tc.typ, tc.checks)
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: row %q missing %q", tc.name, out, w)
			}
		}
		for _, nw := range tc.notWant {
			if strings.Contains(out, nw) {
				t.Errorf("%s: row %q contains %q", tc.name, out, nw)
			}
		}
	}
}

// TestMonitorScreensShowDNSTarget checks the list and detail screens show
// what a DNS monitor queries, since several monitors can share one query
// name and differ only by resolver.
func TestMonitorScreensShowDNSTarget(t *testing.T) {
	mon := ipc.MonitorResponse{
		ID: "01DNS", Name: "ns1 DNS authoritative", Type: "dns", Enabled: true,
		Interval: ipc.Duration(time.Minute), Timeout: ipc.Duration(5 * time.Second),
		Config: json.RawMessage(`{"name":"dysv.de","record_type":"SOA","resolver":"ns1.dysv.de"}`),
	}
	list := newMonitorListScreen(stubClient{})
	scr, _ := list.Update(monitorsLoadedMsg{monitors: []ipc.MonitorResponse{mon}})
	if view := scr.View(); !strings.Contains(view, "TARGET") || !strings.Contains(view, "dysv.de SOA @ ns1.dysv.de") {
		t.Errorf("list view missing the DNS target:\n%s", view)
	}

	detail := newMonitorDetailScreen(stubClient{monitor: mon, checks: []ipc.CheckResultResponse{{
		StartedAt: time.Now(), State: "up", DurationMs: 9,
		Details: json.RawMessage(`{"rcode":"NOERROR","records":["ns1.dysv.de. hostmaster.dysv.de. 2026091901 7200 3600 1209600 3600"]}`),
	}}}, "01DNS")
	view := applyBatch(t, detail, detail.Init()).View()
	for _, want := range []string{"target:    dysv.de SOA @ ns1.dysv.de", "NOERROR", "ns1.dysv.de. hostmaster.dysv.de. 2026091901"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q:\n%s", want, view)
		}
	}
}
