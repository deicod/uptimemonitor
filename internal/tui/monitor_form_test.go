package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// formClient is a fake tui.Client that records the create/update request the
// monitor form submits, so a test can assert the form built the request from
// its field values. It embeds stubClient for the methods it does not exercise.
type formClient struct {
	stubClient
	created *ipc.CreateMonitorRequest
	updated *ipc.UpdateMonitorRequest
	saveErr error
}

var _ Client = (*formClient)(nil)

func (c *formClient) CreateMonitor(_ context.Context, req ipc.CreateMonitorRequest) (ipc.MonitorResponse, error) {
	c.created = &req
	return ipc.MonitorResponse{ID: "new"}, c.saveErr
}

func (c *formClient) UpdateMonitor(_ context.Context, _ string, req ipc.UpdateMonitorRequest) (ipc.MonitorResponse, error) {
	c.updated = &req
	return ipc.MonitorResponse{ID: "edited"}, c.saveErr
}

// TestMonitorFormFieldNavigation verifies tab and shift+tab move the field
// cursor and clamp at the ends, since the cursor is what every other form
// interaction (editing, toggling, error focus) acts on.
func TestMonitorFormFieldNavigation(t *testing.T) {
	s := newMonitorFormScreen(&formClient{}, "")
	s.Init()

	if s.cursor != 0 {
		t.Fatalf("form opens with cursor %d, want 0", s.cursor)
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if s.cursor != 1 {
		t.Fatalf("tab: cursor = %d, want 1", s.cursor)
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if s.cursor != 0 {
		t.Fatalf("shift+tab: cursor = %d, want 0", s.cursor)
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if s.cursor != 0 {
		t.Fatalf("shift+tab past the start: cursor = %d, want 0 (clamped)", s.cursor)
	}
}

// TestMonitorFormRequiredFieldsBlockSubmit verifies a submit with an empty
// required field reports the error against that field and does not call the
// service, so an incomplete monitor is never sent.
func TestMonitorFormRequiredFieldsBlockSubmit(t *testing.T) {
	fc := &formClient{}
	s := newMonitorFormScreen(fc, "")
	s.Init()
	// name and url are empty by default.

	_, cmd := s.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("submit with empty required fields produced a command, want none")
	}
	if s.field("name").fieldErr == "" {
		t.Error("empty name did not set a field error on name")
	}
	if s.field("url").fieldErr == "" {
		t.Error("empty url did not set a field error on url")
	}
	if fc.created != nil {
		t.Error("submit with empty required fields still called CreateMonitor")
	}
}

// TestMonitorFormServerValidationMapsToField verifies a server validation_error
// is shown against the field named in its payload, so the operator sees the
// problem where they can fix it rather than in a generic error dialog.
func TestMonitorFormServerValidationMapsToField(t *testing.T) {
	s := newMonitorFormScreen(&formClient{}, "")
	s.Init()

	apiErr := &ipc.APIError{Code: ipc.ErrValidation, Message: "scheme must be http or https", Field: "url"}
	s.Update(monitorFormErrorMsg{apiErr: apiErr})

	if got := s.field("url").fieldErr; got != "scheme must be http or https" {
		t.Errorf("url field error = %q, want the server message", got)
	}
	if s.field("name").fieldErr != "" {
		t.Error("server error for url leaked onto the name field")
	}
}

// TestMonitorFormCreateBuildsRequest verifies a filled create form submits a
// CreateMonitorRequest carrying the field values, including the HTTP config
// JSON, so the form's inputs become the monitor that is created.
func TestMonitorFormCreateBuildsRequest(t *testing.T) {
	fc := &formClient{}
	s := newMonitorFormScreen(fc, "")
	s.Init()
	s.setText("name", "API")
	s.setText("url", "https://example.com")

	_, cmd := s.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("submit of a valid form produced no command")
	}
	if msg := cmd(); !isSavedMsg(msg) {
		t.Fatalf("save command yielded %T, want monitorSavedMsg", msg)
	}
	if fc.created == nil {
		t.Fatal("submit did not call CreateMonitor")
	}
	req := *fc.created
	if req.Name != "API" || req.Type != "http" {
		t.Errorf("create request name/type = %q/%q, want API/http", req.Name, req.Type)
	}
	if time.Duration(req.Interval) != 60*time.Second || time.Duration(req.Timeout) != 10*time.Second {
		t.Errorf("create request interval/timeout = %s/%s, want 60s/10s",
			time.Duration(req.Interval), time.Duration(req.Timeout))
	}
	var cfg httpFormConfig
	if err := json.Unmarshal(req.Config, &cfg); err != nil {
		t.Fatalf("create request config is not valid JSON: %v", err)
	}
	if cfg.URL != "https://example.com" || cfg.Method != "GET" {
		t.Errorf("config url/method = %q/%q, want https://example.com/GET", cfg.URL, cfg.Method)
	}
	if cfg.ExpectedStatusMin != 200 || cfg.ExpectedStatusMax != 299 {
		t.Errorf("config status range = %d-%d, want 200-299", cfg.ExpectedStatusMin, cfg.ExpectedStatusMax)
	}
}

// TestMonitorFormEditLoadsMonitor verifies the edit form fetches the monitor on
// Init and populates its fields, so editing starts from the stored values.
func TestMonitorFormEditLoadsMonitor(t *testing.T) {
	fc := &formClient{stubClient: stubClient{monitor: ipc.MonitorResponse{
		ID:                   "01A",
		Name:                 "Website",
		Type:                 "http",
		Interval:             ipc.Duration(30 * time.Second),
		Timeout:              ipc.Duration(5 * time.Second),
		Config:               json.RawMessage(`{"url":"https://site.test","method":"GET","expected_status_min":200,"expected_status_max":204}`),
		NotificationsEnabled: true,
	}}}
	s := newMonitorFormScreen(fc, "01A")

	cmd := s.Init()
	if cmd == nil {
		t.Fatal("edit form Init returned no fetch command")
	}
	scr, _ := s.Update(cmd())
	fs := scr.(*monitorFormScreen)
	if !fs.loaded {
		t.Fatal("edit form did not mark itself loaded after the fetch")
	}
	if got := fs.field("name").input.Value(); got != "Website" {
		t.Errorf("name field = %q, want Website", got)
	}
	if got := fs.field("url").input.Value(); got != "https://site.test" {
		t.Errorf("url field = %q, want https://site.test", got)
	}
	if got := fs.field("interval").input.Value(); got != "30s" {
		t.Errorf("interval field = %q, want 30s", got)
	}
}

// TestMonitorFormEditBuildsUpdate verifies the edit form submits an
// UpdateMonitorRequest with the edited values, so an edit reaches the service
// as a partial update.
func TestMonitorFormEditBuildsUpdate(t *testing.T) {
	fc := &formClient{stubClient: stubClient{monitor: ipc.MonitorResponse{
		ID:       "01A",
		Name:     "Website",
		Type:     "http",
		Interval: ipc.Duration(30 * time.Second),
		Timeout:  ipc.Duration(5 * time.Second),
		Config:   json.RawMessage(`{"url":"https://site.test","method":"GET","expected_status_min":200,"expected_status_max":299}`),
	}}}
	s := newMonitorFormScreen(fc, "01A")
	scr, _ := s.Update(s.Init()())
	fs := scr.(*monitorFormScreen)
	fs.setText("name", "Website 2")

	_, cmd := fs.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("edit submit produced no command")
	}
	cmd()
	if fc.updated == nil {
		t.Fatal("edit submit did not call UpdateMonitor")
	}
	if fc.updated.Name == nil || *fc.updated.Name != "Website 2" {
		t.Errorf("update request name = %v, want Website 2", fc.updated.Name)
	}
}

// TestMonitorFormSaveSuccessNavigatesBack verifies a successful save produces a
// command, so the form leaves the screen instead of stranding the operator on
// a submitted form.
func TestMonitorFormSaveSuccessNavigatesBack(t *testing.T) {
	s := newMonitorFormScreen(&formClient{}, "")
	s.Init()
	if _, cmd := s.Update(monitorSavedMsg{}); cmd == nil {
		t.Fatal("save success produced no navigation command")
	}
}

// TestMonitorFormBoolToggle verifies a bool field flips when toggled, so the
// operator can set the enabled and notifications flags.
func TestMonitorFormBoolToggle(t *testing.T) {
	s := newMonitorFormScreen(&formClient{}, "")
	s.Init()
	f := s.field("enabled")
	before := f.on
	for i, fld := range s.fields {
		if fld == f {
			s.cursor = i
		}
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if f.on == before {
		t.Errorf("toggle did not flip the enabled field (still %t)", f.on)
	}
}

// TestMonitorListNewKeyOpensForm verifies the monitor list opens the form when
// it receives the create-navigation message, so the new-monitor key reaches a
// form screen.
func TestMonitorListNewKeyOpensForm(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	_, cmd := s.Update(openMonitorFormMsg{})
	if cmd == nil {
		t.Fatal("form-navigation message produced no command")
	}
	push, ok := cmd().(pushScreenMsg)
	if !ok {
		t.Fatalf("monitor list emitted %T, want pushScreenMsg", cmd())
	}
	if _, ok := push.screen.(*monitorFormScreen); !ok {
		t.Fatalf("monitor list pushed %T, want *monitorFormScreen", push.screen)
	}
}

// TestMonitorListRefreshesOnChange verifies the monitor list re-fetches when it
// is told a monitor changed, so a create or edit is reflected without the
// operator pressing refresh.
func TestMonitorListRefreshesOnChange(t *testing.T) {
	s := newMonitorListScreen(stubClient{monitors: sampleMonitors()})
	_, cmd := s.Update(monitorsChangedMsg{})
	if cmd == nil {
		t.Fatal("monitorsChangedMsg produced no re-fetch command")
	}
	if _, ok := cmd().(monitorsLoadedMsg); !ok {
		t.Fatalf("monitorsChangedMsg did not re-fetch the list, got %T", cmd())
	}
}

// TestMonitorFormViewRendersFields verifies the View renders the field labels
// and a field error, so the operator can read and correct the form.
func TestMonitorFormViewRendersFields(t *testing.T) {
	s := newMonitorFormScreen(&formClient{}, "")
	s.Init()
	s.field("url").fieldErr = "must not be empty"

	view := s.View()
	for _, want := range []string{"name", "url", "method", "interval", "must not be empty"} {
		if !strings.Contains(view, want) {
			t.Errorf("form view missing %q:\n%s", want, view)
		}
	}
}

// isSavedMsg reports whether msg is a monitorSavedMsg.
func isSavedMsg(msg tea.Msg) bool {
	_, ok := msg.(monitorSavedMsg)
	return ok
}
