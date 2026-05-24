package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/notify"
)

// notifyListClient records the target-list actions (test, toggle, delete) so
// the flow tests can assert the right IPC call was made.
type notifyListClient struct {
	stubClient
	tested     string
	setEnabled *bool
	deletedID  string
	deleteErr  error
}

var _ Client = (*notifyListClient)(nil)

func (c *notifyListClient) TestNotificationTarget(_ context.Context, id string) (ipc.TestNotificationResponse, error) {
	c.tested = id
	return ipc.TestNotificationResponse{Sent: true}, nil
}

func (c *notifyListClient) SetNotificationsEnabled(_ context.Context, enabled bool) (bool, error) {
	c.setEnabled = &enabled
	return enabled, nil
}

func (c *notifyListClient) DeleteNotificationTarget(_ context.Context, id string) error {
	c.deletedID = id
	return c.deleteErr
}

// notifyFormClient records the create/update request the form submits.
type notifyFormClient struct {
	stubClient
	createReq *ipc.CreateNotificationTargetRequest
	updateReq *ipc.UpdateNotificationTargetRequest
	saveErr   error
}

var _ Client = (*notifyFormClient)(nil)

func (c *notifyFormClient) CreateNotificationTarget(_ context.Context, req ipc.CreateNotificationTargetRequest) (ipc.NotificationTargetResponse, error) {
	c.createReq = &req
	return ipc.NotificationTargetResponse{ID: "new"}, c.saveErr
}

func (c *notifyFormClient) UpdateNotificationTarget(_ context.Context, _ string, req ipc.UpdateNotificationTargetRequest) (ipc.NotificationTargetResponse, error) {
	c.updateReq = &req
	return ipc.NotificationTargetResponse{ID: "edited"}, c.saveErr
}

// sampleNotificationTargets covers an enabled and a disabled target so the
// list's rendering and selection are exercised.
func sampleNotificationTargets() []ipc.NotificationTargetResponse {
	return []ipc.NotificationTargetResponse{
		{ID: "01A", Name: "Ops Slack", Kind: "slack", Enabled: true},
		{ID: "01B", Name: "Email", Kind: "email", Enabled: false},
	}
}

// sampleProviders is provider metadata covering a secret field, a defaulted
// field, and a second kind so the form's dynamic rendering and kind switching
// are exercised.
func sampleProviders() []ipc.NotificationProviderResponse {
	return []ipc.NotificationProviderResponse{
		{Kind: "webhook", DisplayName: "Webhook", Fields: []notify.Field{
			{Name: "url", Label: "URL", Type: notify.FieldTypeSecretString, Required: true, Secret: true},
			{Name: "method", Label: "Method", Type: notify.FieldTypeString, Required: true, Default: "POST"},
		}},
		{Kind: "gotify", DisplayName: "Gotify", Fields: []notify.Field{
			{Name: "server_url", Label: "Server URL", Type: notify.FieldTypeURL, Required: true},
			{Name: "token", Label: "Token", Type: notify.FieldTypeSecretString, Secret: true},
		}},
	}
}

// ---------- target list ----------

// TestNotificationListLoadsAndRenders verifies the list fetches targets and the
// global toggle on Init and renders both, so the operator sees the configured
// targets and whether notifications are globally on.
func TestNotificationListLoadsAndRenders(t *testing.T) {
	c := stubClient{targets: sampleNotificationTargets(), notificationsEnabled: true}
	s := newNotificationTargetListScreen(c)

	scr := applyBatch(t, s, s.Init())
	ls := scr.(*notificationTargetListScreen)
	if len(ls.targets) != 2 {
		t.Fatalf("targets cached = %d, want 2", len(ls.targets))
	}
	if !ls.globalEnabled {
		t.Error("global toggle not cached as enabled")
	}
	view := ls.View()
	for _, want := range []string{"NAME", "KIND", "Ops Slack", "slack", "global notifications: on"} {
		if !strings.Contains(view, want) {
			t.Errorf("target list view missing %q:\n%s", want, view)
		}
	}
}

// TestNotificationListNewKeyOpensForm verifies the new key navigates to a create
// form (empty target ID).
func TestNotificationListNewKeyOpensForm(t *testing.T) {
	s := newNotificationTargetListScreen(stubClient{})
	_, cmd := s.Update(runeKey('n'))
	msg, ok := cmd().(openNotificationFormMsg)
	if !ok {
		t.Fatalf("new key emitted %T, want openNotificationFormMsg", cmd())
	}
	if msg.targetID != "" {
		t.Errorf("create navigation carries target ID %q, want empty", msg.targetID)
	}
	// And the navigation message pushes the form screen.
	_, cmd = s.Update(openNotificationFormMsg{})
	push, ok := cmd().(pushScreenMsg)
	if !ok {
		t.Fatalf("form navigation emitted %T, want pushScreenMsg", cmd())
	}
	if _, ok := push.screen.(*notificationFormScreen); !ok {
		t.Fatalf("pushed %T, want *notificationFormScreen", push.screen)
	}
}

// TestNotificationListEditKeyOpensForm verifies enter edits the selected target.
func TestNotificationListEditKeyOpensForm(t *testing.T) {
	s := newNotificationTargetListScreen(stubClient{})
	s.Update(notificationTargetsLoadedMsg{targets: sampleNotificationTargets()})
	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	_, cmd := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg, ok := cmd().(openNotificationFormMsg)
	if !ok {
		t.Fatalf("edit key emitted %T, want openNotificationFormMsg", cmd())
	}
	if msg.targetID != "01B" {
		t.Errorf("edit navigation carries target ID %q, want 01B", msg.targetID)
	}
}

// TestNotificationListDeleteOpensConfirm verifies delete passes through a
// confirmation dialog that names the target, and does not delete before
// confirmation (SPEC §19.4).
func TestNotificationListDeleteOpensConfirm(t *testing.T) {
	lc := &notifyListClient{stubClient: stubClient{targets: sampleNotificationTargets()}}
	s := newNotificationTargetListScreen(lc)
	s.Update(notificationTargetsLoadedMsg{targets: sampleNotificationTargets()})

	_, cmd := s.Update(runeKey('d'))
	push, ok := cmd().(pushScreenMsg)
	if !ok {
		t.Fatalf("delete key emitted %T, want pushScreenMsg", cmd())
	}
	cs, ok := push.screen.(*confirmScreen)
	if !ok {
		t.Fatalf("delete pushed %T, want *confirmScreen", push.screen)
	}
	if !strings.Contains(cs.View(), "Ops Slack") {
		t.Errorf("confirm prompt does not name the target:\n%s", cs.View())
	}
	if lc.deletedID != "" {
		t.Error("delete reached the service before confirmation")
	}
}

// TestNotificationListTestSend verifies the test key sends a test notification
// and shows transient feedback (SPEC §18.9 send-test).
func TestNotificationListTestSend(t *testing.T) {
	lc := &notifyListClient{stubClient: stubClient{targets: sampleNotificationTargets()}}
	s := newNotificationTargetListScreen(lc)
	s.Update(notificationTargetsLoadedMsg{targets: sampleNotificationTargets()})

	_, cmd := s.Update(runeKey('t'))
	if cmd == nil {
		t.Fatal("test key produced no command")
	}
	msg := cmd()
	sent, ok := msg.(notificationTestSentMsg)
	if !ok {
		t.Fatalf("test key yielded %T, want notificationTestSentMsg", msg)
	}
	if lc.tested != "01A" {
		t.Errorf("TestNotificationTarget called with %q, want 01A", lc.tested)
	}
	s.Update(sent)
	if !strings.Contains(s.View(), "test notification queued") {
		t.Errorf("list does not show test feedback:\n%s", s.View())
	}
}

// TestNotificationListToggleGlobal verifies the toggle key flips the global
// setting to the opposite of its current value (SPEC §18.6).
func TestNotificationListToggleGlobal(t *testing.T) {
	lc := &notifyListClient{stubClient: stubClient{notificationsEnabled: true}}
	s := newNotificationTargetListScreen(lc)
	scr := applyBatch(t, s, s.Init())
	ls := scr.(*notificationTargetListScreen)

	_, cmd := ls.Update(runeKey('g'))
	if cmd == nil {
		t.Fatal("toggle key produced no command")
	}
	msg := cmd()
	loaded, ok := msg.(notificationGlobalLoadedMsg)
	if !ok {
		t.Fatalf("toggle yielded %T, want notificationGlobalLoadedMsg", msg)
	}
	if loaded.enabled {
		t.Error("toggle did not flip the global state to off")
	}
	if lc.setEnabled == nil || *lc.setEnabled {
		t.Errorf("SetNotificationsEnabled called with %v, want false", lc.setEnabled)
	}
}

// TestNotificationListAttemptsKey verifies the attempts key navigates to the
// attempts list.
func TestNotificationListAttemptsKey(t *testing.T) {
	s := newNotificationTargetListScreen(stubClient{})
	_, cmd := s.Update(runeKey('a'))
	if _, ok := cmd().(openNotificationAttemptsMsg); !ok {
		t.Fatalf("attempts key emitted %T, want openNotificationAttemptsMsg", cmd())
	}
	_, cmd = s.Update(openNotificationAttemptsMsg{})
	push, ok := cmd().(pushScreenMsg)
	if !ok {
		t.Fatalf("attempts navigation emitted %T, want pushScreenMsg", cmd())
	}
	if _, ok := push.screen.(*notificationAttemptsScreen); !ok {
		t.Fatalf("pushed %T, want *notificationAttemptsScreen", push.screen)
	}
}

// TestDeleteNotificationTargetCmdSuccess verifies a confirmed delete calls the
// service and signals a re-fetch.
func TestDeleteNotificationTargetCmdSuccess(t *testing.T) {
	lc := &notifyListClient{}
	if _, ok := deleteNotificationTargetCmd(lc, "01A")().(notificationTargetsChangedMsg); !ok {
		t.Fatal("delete success did not emit notificationTargetsChangedMsg")
	}
	if lc.deletedID != "01A" {
		t.Errorf("delete called with %q, want 01A", lc.deletedID)
	}
}

// TestDeleteNotificationTargetCmdFailure verifies a delete failure surfaces
// through the error dialog (SPEC §19.4).
func TestDeleteNotificationTargetCmdFailure(t *testing.T) {
	lc := &notifyListClient{deleteErr: errors.New("boom")}
	em, ok := deleteNotificationTargetCmd(lc, "01A")().(errMsg)
	if !ok {
		t.Fatalf("delete failure produced %T, want errMsg", deleteNotificationTargetCmd(lc, "01A")())
	}
	if em.err == nil || !strings.Contains(em.err.Error(), "boom") {
		t.Errorf("errMsg does not carry the underlying error: %v", em.err)
	}
}

// ---------- provider-driven form ----------

// TestNotificationFormRendersProviderFields verifies the create form renders the
// fields advertised by the default-selected provider (SPEC §18.4).
func TestNotificationFormRendersProviderFields(t *testing.T) {
	s := newNotificationFormScreen(stubClient{providers: sampleProviders()}, "")
	scr := applyBatch(t, s, s.Init())
	fs := scr.(*notificationFormScreen)
	if !fs.loaded {
		t.Fatal("form did not load")
	}
	view := fs.View()
	for _, want := range []string{"name", "kind", "enabled", "webhook", "URL", "Method"} {
		if !strings.Contains(view, want) {
			t.Errorf("form view missing %q:\n%s", want, view)
		}
	}
}

// TestNotificationFormKindSwitchRebuildsFields verifies cycling the kind select
// replaces the dynamic fields with the newly-selected provider's fields.
func TestNotificationFormKindSwitchRebuildsFields(t *testing.T) {
	s := newNotificationFormScreen(stubClient{providers: sampleProviders()}, "")
	scr := applyBatch(t, s, s.Init())
	fs := scr.(*notificationFormScreen)

	if fs.field("url") == nil || fs.field("server_url") != nil {
		t.Fatal("default kind should render webhook fields, not gotify")
	}
	// Move the cursor onto the kind select (meta index 1) and cycle it.
	fs.cursor = 1
	fs.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	if fs.field("server_url") == nil {
		t.Error("kind switch did not load gotify's fields")
	}
	if fs.field("url") != nil {
		t.Error("webhook's url field still present after kind switch")
	}
}

// TestNotificationFormCreateBuildsRequest verifies a filled create form submits
// a request carrying the kind and a config built from the dynamic fields.
func TestNotificationFormCreateBuildsRequest(t *testing.T) {
	fc := &notifyFormClient{stubClient: stubClient{providers: sampleProviders()}}
	s := newNotificationFormScreen(fc, "")
	scr := applyBatch(t, s, s.Init())
	fs := scr.(*notificationFormScreen)
	fs.field("name").input.SetValue("Hook")
	fs.field("url").input.SetValue("https://hook.test")

	_, cmd := fs.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("submit of a valid form produced no command")
	}
	if _, ok := cmd().(notificationSavedMsg); !ok {
		t.Fatalf("save yielded %T, want notificationSavedMsg", cmd())
	}
	if fc.createReq == nil {
		t.Fatal("submit did not call CreateNotificationTarget")
	}
	req := *fc.createReq
	if req.Name != "Hook" || req.Kind != "webhook" {
		t.Errorf("request name/kind = %q/%q, want Hook/webhook", req.Name, req.Kind)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(req.Config, &cfg); err != nil {
		t.Fatalf("config not valid JSON: %v", err)
	}
	if cfg["url"] != "https://hook.test" || cfg["method"] != "POST" {
		t.Errorf("config = %v, want url=https://hook.test method=POST", cfg)
	}
}

// TestNotificationFormCreateRequiresSecret verifies a blank required secret
// blocks a create submit, since there is no stored value to fall back on.
func TestNotificationFormCreateRequiresSecret(t *testing.T) {
	fc := &notifyFormClient{stubClient: stubClient{providers: sampleProviders()}}
	s := newNotificationFormScreen(fc, "")
	scr := applyBatch(t, s, s.Init())
	fs := scr.(*notificationFormScreen)
	fs.field("name").input.SetValue("Hook")
	// url (required secret) left blank.

	_, cmd := fs.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("submit with a blank required secret produced a command, want none")
	}
	if fs.field("url").fieldErr == "" {
		t.Error("blank required secret did not set a field error")
	}
	if fc.createReq != nil {
		t.Error("submit reached the service despite the missing secret")
	}
}

// TestNotificationFormEditShowsSecretState verifies an edit form shows a secret
// as set when one is stored and unset otherwise (SPEC §18.9).
func TestNotificationFormEditShowsSecretState(t *testing.T) {
	set := &notifyFormClient{stubClient: stubClient{
		providers: sampleProviders(),
		target: ipc.NotificationTargetResponse{
			ID: "01G", Name: "Ops", Kind: "gotify", Enabled: true,
			Config: json.RawMessage(`{"server_url":"https://g","token":""}`),
		},
	}}
	fs := applyBatch(t, newNotificationFormScreen(set, "01G"), newNotificationFormScreen(set, "01G").Init()).(*notificationFormScreen)
	if !strings.Contains(fs.View(), "blank keeps current") {
		t.Errorf("stored secret not shown as set:\n%s", fs.View())
	}

	unset := &notifyFormClient{stubClient: stubClient{
		providers: sampleProviders(),
		target: ipc.NotificationTargetResponse{
			ID: "01G", Name: "Ops", Kind: "gotify", Enabled: true,
			Config: json.RawMessage(`{"server_url":"https://g"}`),
		},
	}}
	fs2 := applyBatch(t, newNotificationFormScreen(unset, "01G"), newNotificationFormScreen(unset, "01G").Init()).(*notificationFormScreen)
	if !strings.Contains(fs2.View(), "(unset)") {
		t.Errorf("missing secret not shown as unset:\n%s", fs2.View())
	}
}

// TestNotificationFormEditOmitsBlankSecret verifies an edit that leaves a secret
// blank omits it from the submitted config, so the service preserves the stored
// value rather than overwriting it (SPEC §18.9).
func TestNotificationFormEditOmitsBlankSecret(t *testing.T) {
	fc := &notifyFormClient{stubClient: stubClient{
		providers: sampleProviders(),
		target: ipc.NotificationTargetResponse{
			ID: "01G", Name: "Ops", Kind: "gotify", Enabled: true,
			Config: json.RawMessage(`{"server_url":"https://g","token":""}`),
		},
	}}
	s := newNotificationFormScreen(fc, "01G")
	scr := applyBatch(t, s, s.Init())
	fs := scr.(*notificationFormScreen)
	fs.field("server_url").input.SetValue("https://g2") // token left blank

	_, cmd := fs.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("edit submit produced no command")
	}
	cmd()
	if fc.updateReq == nil {
		t.Fatal("edit submit did not call UpdateNotificationTarget")
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(fc.updateReq.Config, &cfg); err != nil {
		t.Fatalf("config not valid JSON: %v", err)
	}
	if _, has := cfg["token"]; has {
		t.Errorf("blank secret was not omitted from the update config: %v", cfg)
	}
	if cfg["server_url"] != "https://g2" {
		t.Errorf("public field not updated: %v", cfg)
	}
}

// TestNotificationFormServerValidationMapsToField verifies a server
// validation_error is shown against the named field (SPEC §10.3).
func TestNotificationFormServerValidationMapsToField(t *testing.T) {
	s := newNotificationFormScreen(stubClient{providers: sampleProviders()}, "")
	scr := applyBatch(t, s, s.Init())
	fs := scr.(*notificationFormScreen)

	fs.Update(notificationFormErrorMsg{apiErr: &ipc.APIError{
		Code: ipc.ErrValidation, Message: "name is required", Field: "name",
	}})
	if got := fs.field("name").fieldErr; got != "name is required" {
		t.Errorf("name field error = %q, want the server message", got)
	}
}

// ---------- attempts list ----------

// TestNotificationAttemptsLoadsAndRenders verifies the attempts screen fetches
// attempts and resolves target names for display.
func TestNotificationAttemptsLoadsAndRenders(t *testing.T) {
	c := stubClient{
		attempts: []ipc.NotificationAttemptResponse{
			{ID: "a1", TargetID: "01A", EventType: "monitor_down", Status: "failure",
				AttemptNumber: 2, Error: "connection refused", CreatedAt: time.Now()},
		},
		targets: []ipc.NotificationTargetResponse{{ID: "01A", Name: "Ops Slack"}},
	}
	s := newNotificationAttemptsScreen(c)
	scr := applyBatch(t, s, s.Init())
	as := scr.(*notificationAttemptsScreen)
	if len(as.attempts) != 1 {
		t.Fatalf("attempts cached = %d, want 1", len(as.attempts))
	}
	view := as.View()
	for _, want := range []string{"monitor_down", "failure", "Ops Slack", "connection refused"} {
		if !strings.Contains(view, want) {
			t.Errorf("attempts view missing %q:\n%s", want, view)
		}
	}
}

// TestHomeNavigatesToNotifications verifies the home screen opens the
// notification target list on its navigation key.
func TestHomeNavigatesToNotifications(t *testing.T) {
	s := newHomeScreen(stubClient{})
	_, cmd := s.Update(runeKey('N'))
	if cmd == nil {
		t.Fatal("notifications navigation key produced no command")
	}
	push, ok := cmd().(pushScreenMsg)
	if !ok {
		t.Fatalf("home did not push a screen, got %T", cmd())
	}
	if _, ok := push.screen.(*notificationTargetListScreen); !ok {
		t.Fatalf("home pushed %T, want *notificationTargetListScreen", push.screen)
	}
}
