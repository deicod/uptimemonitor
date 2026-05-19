package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// monitorFormLoadedMsg delivers the monitor an edit form fetched so it can
// populate its fields from the stored values.
type monitorFormLoadedMsg struct{ monitor ipc.MonitorResponse }

// monitorSavedMsg signals the form's create or update succeeded.
type monitorSavedMsg struct{}

// monitorFormErrorMsg carries a server-side validation_error back to the form
// so it is shown against the matching field rather than in the error dialog
// (SPEC §10.3 validation_error.field).
type monitorFormErrorMsg struct{ apiErr *ipc.APIError }

// monitorsChangedMsg tells the monitor list screen to re-fetch because a
// monitor was created or edited.
type monitorsChangedMsg struct{}

// Monitor form key bindings, in addition to the global keymap. tab/↑↓ move the
// field cursor; the text widgets do not consume those keys, so navigation
// works even while a field is being edited.
var (
	formNextKey   = key.NewBinding(key.WithKeys("tab", "down"))
	formPrevKey   = key.NewBinding(key.WithKeys("shift+tab", "up"))
	formSaveKey   = key.NewBinding(key.WithKeys("ctrl+s"))
	formToggleKey = key.NewBinding(key.WithKeys(" ", "space", "left", "right", "enter"))
)

// formErrStyle renders field and form-level validation errors.
var formErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

// httpFormConfig is the HTTP monitor configuration the form reads and writes.
// It mirrors the SPEC §11.2 HTTPMonitorConfig JSON shape; the TUI keeps its own
// copy so it does not depend on the monitor domain package.
type httpFormConfig struct {
	URL               string `json:"url"`
	Method            string `json:"method"`
	ExpectedStatusMin int    `json:"expected_status_min"`
	ExpectedStatusMax int    `json:"expected_status_max"`
}

// formFieldKind distinguishes a text-entry field from an on/off toggle.
type formFieldKind int

const (
	fieldText formFieldKind = iota
	fieldBool
)

// formField is one editable row on the monitor form. key matches the
// validation_error.field name the service returns, so a server error can be
// routed back to the field that produced it.
type formField struct {
	key      string
	label    string
	kind     formFieldKind
	input    textinput.Model
	on       bool
	fieldErr string
}

// monitorFormScreen is the create/edit form for an HTTP monitor (SPEC §11.1–
// 11.2, §12.3). An empty monitorID means create; a non-empty one means edit,
// in which case the screen first fetches the monitor to seed its fields.
type monitorFormScreen struct {
	client    Client
	monitorID string
	editing   bool
	loaded    bool
	fields    []*formField
	cursor    int
	formErr   string
}

// newMonitorFormScreen builds the form bound to client. monitorID empty creates
// a new monitor; a non-empty monitorID edits that monitor.
func newMonitorFormScreen(client Client, monitorID string) *monitorFormScreen {
	editing := monitorID != ""
	s := &monitorFormScreen{
		client:    client,
		monitorID: monitorID,
		editing:   editing,
		fields:    defaultFormFields(editing),
	}
	// A create form has nothing to fetch, so it is ready immediately.
	s.loaded = !editing
	return s
}

// defaultFormFields builds the form's fields with create-time defaults. The
// enabled toggle appears only when creating: an edit cannot change the enabled
// flag, which is driven by the separate enable/disable operation (SPEC §10.5).
func defaultFormFields(editing bool) []*formField {
	fields := []*formField{
		newTextField("name", "name", ""),
		newTextField("url", "url", ""),
		newTextField("method", "method", "GET"),
		newTextField("expected_status_min", "expected status min", "200"),
		newTextField("expected_status_max", "expected status max", "299"),
		newTextField("interval", "interval", "60s"),
		newTextField("timeout", "timeout", "10s"),
	}
	if !editing {
		fields = append(fields, newBoolField("enabled", "enabled", true))
	}
	fields = append(fields, newBoolField("notifications_enabled", "notifications enabled", true))
	return fields
}

// newTextField builds a text-entry field seeded with value.
func newTextField(key, label, value string) *formField {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(value)
	return &formField{key: key, label: label, kind: fieldText, input: ti}
}

// newBoolField builds an on/off toggle field.
func newBoolField(key, label string, on bool) *formField {
	return &formField{key: key, label: label, kind: fieldBool, on: on}
}

// Init fetches the monitor when editing; for a create form it just focuses the
// first field (SPEC §19.3).
func (s *monitorFormScreen) Init() tea.Cmd {
	if s.editing {
		return fetchMonitorFormCmd(s.client, s.monitorID)
	}
	return s.syncFocus()
}

// Update handles the fetched monitor, the save outcome, and key input.
func (s *monitorFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case monitorFormLoadedMsg:
		s.applyMonitor(msg.monitor)
		s.loaded = true
		return s, s.syncFocus()
	case monitorSavedMsg:
		// Return to the list and tell it to re-fetch, in that order.
		return s, tea.Sequence(PopScreen, emitMonitorsChanged)
	case monitorFormErrorMsg:
		s.applyServerError(msg.apiErr)
		return s, nil
	case tea.KeyPressMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

// handleKey applies a key press: field navigation, save, a toggle on a bool
// field, or text entry routed to the focused field's input.
func (s *monitorFormScreen) handleKey(msg tea.KeyPressMsg) (Screen, tea.Cmd) {
	if !s.loaded {
		return s, nil
	}
	switch {
	case key.Matches(msg, formSaveKey):
		return s, s.submit()
	case key.Matches(msg, formNextKey):
		s.moveCursor(1)
		return s, s.syncFocus()
	case key.Matches(msg, formPrevKey):
		s.moveCursor(-1)
		return s, s.syncFocus()
	}
	f := s.fields[s.cursor]
	if f.kind == fieldBool {
		if key.Matches(msg, formToggleKey) {
			f.on = !f.on
		}
		return s, nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return s, cmd
}

// moveCursor shifts the field cursor by delta, clamped to the field range.
func (s *monitorFormScreen) moveCursor(delta int) {
	s.cursor += delta
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= len(s.fields) {
		s.cursor = len(s.fields) - 1
	}
}

// syncFocus focuses the text input under the cursor and blurs the rest,
// returning the focused input's cursor-blink command.
func (s *monitorFormScreen) syncFocus() tea.Cmd {
	var cmd tea.Cmd
	for i, f := range s.fields {
		if f.kind != fieldText {
			continue
		}
		if i == s.cursor {
			cmd = f.input.Focus()
		} else {
			f.input.Blur()
		}
	}
	return cmd
}

// submit validates the form client-side and, if it is clean, returns the
// create or update command. A client-side failure marks the offending fields
// and returns nil so nothing is sent (SPEC §24.3 form validation).
func (s *monitorFormScreen) submit() tea.Cmd {
	for _, f := range s.fields {
		f.fieldErr = ""
	}
	s.formErr = ""

	name := strings.TrimSpace(s.text("name"))
	urlStr := strings.TrimSpace(s.text("url"))
	method := strings.TrimSpace(s.text("method"))

	ok := true
	if name == "" {
		s.setErr("name", "must not be empty")
		ok = false
	}
	if urlStr == "" {
		s.setErr("url", "must not be empty")
		ok = false
	}
	if method == "" {
		s.setErr("method", "must not be empty")
		ok = false
	}
	statusMin, ok1 := s.parseInt("expected_status_min")
	statusMax, ok2 := s.parseInt("expected_status_max")
	interval, ok3 := s.parseDuration("interval")
	timeout, ok4 := s.parseDuration("timeout")
	if !ok || !ok1 || !ok2 || !ok3 || !ok4 {
		return nil
	}

	cfg, err := json.Marshal(httpFormConfig{
		URL:               urlStr,
		Method:            method,
		ExpectedStatusMin: statusMin,
		ExpectedStatusMax: statusMax,
	})
	if err != nil {
		s.formErr = "could not encode the HTTP configuration"
		return nil
	}

	if s.editing {
		return updateMonitorCmd(s.client, s.monitorID, ipc.UpdateMonitorRequest{
			Name:                 &name,
			Interval:             durationPtr(interval),
			Timeout:              durationPtr(timeout),
			Config:               cfg,
			NotificationsEnabled: boolPtr(s.boolVal("notifications_enabled")),
		})
	}
	return createMonitorCmd(s.client, ipc.CreateMonitorRequest{
		Name:                 name,
		Type:                 "http",
		Enabled:              s.boolVal("enabled"),
		Interval:             ipc.Duration(interval),
		Timeout:              ipc.Duration(timeout),
		Config:               cfg,
		NotificationsEnabled: s.boolVal("notifications_enabled"),
	})
}

// applyMonitor seeds the form fields from a fetched monitor for editing.
func (s *monitorFormScreen) applyMonitor(m ipc.MonitorResponse) {
	s.setText("name", m.Name)
	s.setText("interval", time.Duration(m.Interval).String())
	s.setText("timeout", time.Duration(m.Timeout).String())
	s.setBool("notifications_enabled", m.NotificationsEnabled)

	var cfg httpFormConfig
	if err := json.Unmarshal(m.Config, &cfg); err == nil {
		s.setText("url", cfg.URL)
		if cfg.Method != "" {
			s.setText("method", cfg.Method)
		}
		s.setText("expected_status_min", strconv.Itoa(cfg.ExpectedStatusMin))
		s.setText("expected_status_max", strconv.Itoa(cfg.ExpectedStatusMax))
	}
}

// applyServerError shows a server validation_error against the field it names,
// moving the cursor there; an unrecognised field falls back to a form-level
// error so the message is never lost (SPEC §10.3).
func (s *monitorFormScreen) applyServerError(apiErr *ipc.APIError) {
	for i, f := range s.fields {
		if f.key == apiErr.Field && apiErr.Field != "" {
			f.fieldErr = apiErr.Message
			s.cursor = i
			s.syncFocus()
			return
		}
	}
	s.formErr = apiErr.Message
}

// View renders the form fields with the cursor, current values, and any
// validation errors.
func (s *monitorFormScreen) View() string {
	var b strings.Builder
	b.WriteString(s.Title() + "\n\n")
	if !s.loaded {
		b.WriteString("loading monitor…")
		return b.String()
	}
	for i, f := range s.fields {
		cursor := "  "
		if i == s.cursor {
			cursor = "› "
		}
		fmt.Fprintf(&b, "%s%-22s ", cursor, f.label)
		if f.kind == fieldBool {
			b.WriteString(checkbox(f.on))
		} else {
			b.WriteString(f.input.View())
		}
		if f.fieldErr != "" {
			b.WriteString("  " + formErrStyle.Render("✗ "+f.fieldErr))
		}
		b.WriteString("\n")
	}
	if s.formErr != "" {
		b.WriteString("\n" + formErrStyle.Render(s.formErr) + "\n")
	}
	b.WriteString("\ntype is fixed to http for MVP")
	b.WriteString("\ntab/↑↓ move • space toggle • ctrl+s save • esc cancel")
	return b.String()
}

// Title is the screen name shown in the status bar.
func (s *monitorFormScreen) Title() string {
	if s.editing {
		return "Edit Monitor"
	}
	return "New Monitor"
}

// checkbox renders a bool field's value.
func checkbox(on bool) string {
	if on {
		return "[x]"
	}
	return "[ ]"
}

// field returns the field with the given key, or nil if there is none.
func (s *monitorFormScreen) field(key string) *formField {
	for _, f := range s.fields {
		if f.key == key {
			return f
		}
	}
	return nil
}

// text returns the current value of a text field.
func (s *monitorFormScreen) text(key string) string { return s.field(key).input.Value() }

// setText sets a text field's value.
func (s *monitorFormScreen) setText(key, value string) { s.field(key).input.SetValue(value) }

// boolVal returns a bool field's value.
func (s *monitorFormScreen) boolVal(key string) bool { return s.field(key).on }

// setBool sets a bool field's value.
func (s *monitorFormScreen) setBool(key string, on bool) { s.field(key).on = on }

// setErr records a validation error against a field.
func (s *monitorFormScreen) setErr(key, msg string) { s.field(key).fieldErr = msg }

// parseInt parses a numeric field, marking it on failure.
func (s *monitorFormScreen) parseInt(key string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(s.text(key)))
	if err != nil {
		s.setErr(key, "must be a whole number")
		return 0, false
	}
	return v, true
}

// parseDuration parses a duration field, marking it on failure.
func (s *monitorFormScreen) parseDuration(key string) (time.Duration, bool) {
	v, err := time.ParseDuration(strings.TrimSpace(s.text(key)))
	if err != nil {
		s.setErr(key, "must be a duration like 60s")
		return 0, false
	}
	return v, true
}

// durationPtr returns a pointer to the IPC duration form of d.
func durationPtr(d time.Duration) *ipc.Duration {
	v := ipc.Duration(d)
	return &v
}

// boolPtr returns a pointer to b.
func boolPtr(b bool) *bool { return &b }

// emitMonitorsChanged is a command that signals the monitor list to re-fetch.
func emitMonitorsChanged() tea.Msg { return monitorsChangedMsg{} }

// fetchMonitorFormCmd fetches the monitor an edit form needs (SPEC §19.3).
func fetchMonitorFormCmd(c Client, id string) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) (ipc.MonitorResponse, error) { return c.GetMonitor(ctx, id) },
		func(m ipc.MonitorResponse) tea.Msg { return monitorFormLoadedMsg{monitor: m} },
	)
}

// createMonitorCmd submits a new monitor over IPC. A server validation_error is
// returned as monitorFormErrorMsg so the form can show it on the right field;
// any other failure becomes an errMsg for the error dialog (SPEC §19.3–19.4).
func createMonitorCmd(c Client, req ipc.CreateMonitorRequest) tea.Cmd {
	return func() tea.Msg {
		_, err := c.CreateMonitor(context.Background(), req)
		return saveResultMsg(err)
	}
}

// updateMonitorCmd submits a partial monitor update over IPC, mapping errors
// the same way as createMonitorCmd.
func updateMonitorCmd(c Client, id string, req ipc.UpdateMonitorRequest) tea.Cmd {
	return func() tea.Msg {
		_, err := c.UpdateMonitor(context.Background(), id, req)
		return saveResultMsg(err)
	}
}

// saveResultMsg turns a create/update outcome into the right message: success,
// a field-scoped validation error, or a generic error for the dialog.
func saveResultMsg(err error) tea.Msg {
	if err == nil {
		return monitorSavedMsg{}
	}
	var apiErr *ipc.APIError
	if errors.As(err, &apiErr) && apiErr.Code == ipc.ErrValidation {
		return monitorFormErrorMsg{apiErr: apiErr}
	}
	return errMsg{err: err}
}
