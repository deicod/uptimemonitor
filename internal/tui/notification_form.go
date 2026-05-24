package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/notify"
)

// notificationProvidersLoadedMsg delivers the provider field metadata the form
// needs to render its inputs.
type notificationProvidersLoadedMsg struct {
	providers []ipc.NotificationProviderResponse
}

// notificationTargetLoadedMsg delivers the target an edit form is seeding from.
type notificationTargetLoadedMsg struct {
	target ipc.NotificationTargetResponse
}

// notificationSavedMsg signals the form's create or update succeeded.
type notificationSavedMsg struct{}

// notificationFormErrorMsg carries a server-side validation_error back to the
// form so it shows against the matching field (SPEC §10.3).
type notificationFormErrorMsg struct{ apiErr *ipc.APIError }

// nfFieldKind distinguishes the widget a form row renders.
type nfFieldKind int

const (
	nfText nfFieldKind = iota
	nfBool
	nfSelect
)

// nfField is one editable row on the notification form. name is the config key
// (or "name"/"kind"/"enabled" for the meta rows) and matches the
// validation_error.field the service returns.
type nfField struct {
	name     string
	label    string
	kind     nfFieldKind
	ftype    notify.FieldType
	required bool
	secret   bool
	wasSet   bool // edit: a secret value is already stored for this field
	input    textinput.Model
	on       bool
	choices  []string
	choice   int
	fieldErr string
}

// notificationFormScreen is the provider-driven create/edit form for a
// notification target (SPEC §12.4, §18.4, §18.9). It renders the fields the
// selected provider advertises via /v1/notifications/providers; secret fields
// are shown as set/unset and left blank to preserve the stored value.
type notificationFormScreen struct {
	client          Client
	targetID        string
	editing         bool
	providersLoaded bool
	targetLoaded    bool
	loaded          bool

	providers []ipc.NotificationProviderResponse
	target    ipc.NotificationTargetResponse
	targetCfg map[string]any

	meta    []*nfField
	dynamic []*nfField
	fields  []*nfField
	cursor  int
	formErr string
}

// newNotificationFormScreen builds the form bound to client. An empty targetID
// creates a target; a non-empty one edits it.
func newNotificationFormScreen(client Client, targetID string) *notificationFormScreen {
	return &notificationFormScreen{
		client:   client,
		targetID: targetID,
		editing:  targetID != "",
	}
}

// Init fetches the provider metadata, plus the target when editing (SPEC §19.3).
func (s *notificationFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{fetchNotificationProvidersCmd(s.client)}
	if s.editing {
		cmds = append(cmds, fetchNotificationTargetCmd(s.client, s.targetID))
	}
	return tea.Batch(cmds...)
}

// Update handles loaded data, the save outcome, and key input.
func (s *notificationFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case notificationProvidersLoadedMsg:
		s.providers = msg.providers
		s.providersLoaded = true
		return s, s.maybeBuild()
	case notificationTargetLoadedMsg:
		s.target = msg.target
		s.targetCfg = parseConfigMap(msg.target.Config)
		s.targetLoaded = true
		return s, s.maybeBuild()
	case notificationSavedMsg:
		return s, tea.Sequence(PopScreen, emitNotificationTargetsChanged)
	case notificationFormErrorMsg:
		s.applyServerError(msg.apiErr)
		return s, nil
	case tea.KeyPressMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

// maybeBuild constructs the form once its prerequisites have loaded.
func (s *notificationFormScreen) maybeBuild() tea.Cmd {
	if !s.providersLoaded || (s.editing && !s.targetLoaded) {
		return nil
	}
	s.buildFields()
	s.loaded = true
	return s.syncFocus()
}

// buildFields assembles the meta rows (name, kind-on-create, enabled) and the
// provider-specific rows for the current kind.
func (s *notificationFormScreen) buildFields() {
	name := ""
	enabled := true
	if s.editing {
		name = s.target.Name
		enabled = s.target.Enabled
	}
	s.meta = []*nfField{newNFText("name", "name", name, true)}
	if !s.editing {
		kinds := s.providerKinds()
		s.meta = append(s.meta, &nfField{name: "kind", label: "kind", kind: nfSelect, choices: kinds})
	}
	s.meta = append(s.meta, &nfField{name: "enabled", label: "enabled", kind: nfBool, on: enabled})
	s.rebuildDynamic()
}

// rebuildDynamic rebuilds the provider-specific rows for the current kind,
// leaving the meta rows untouched (used when the kind select changes).
func (s *notificationFormScreen) rebuildDynamic() {
	prov := s.currentProvider()
	s.dynamic = nil
	if prov != nil {
		for _, f := range prov.Fields {
			s.dynamic = append(s.dynamic, s.newProviderField(f))
		}
	}
	s.fields = append(append([]*nfField{}, s.meta...), s.dynamic...)
}

// newProviderField builds a form row from a provider's field metadata, seeding
// it from the field default (create) or the target's stored config (edit).
func (s *notificationFormScreen) newProviderField(f notify.Field) *nfField {
	label := f.Label
	if label == "" {
		label = f.Name
	}
	if f.Type == notify.FieldTypeBool {
		on := f.Default == "true"
		if s.editing {
			if v, ok := s.targetCfg[f.Name].(bool); ok {
				on = v
			}
		}
		return &nfField{name: f.Name, label: label, kind: nfBool, ftype: f.Type, required: f.Required, on: on}
	}
	nf := newNFText(f.Name, label, "", f.Required)
	nf.ftype = f.Type
	nf.secret = f.Secret
	if s.editing {
		if f.Secret {
			// The redacted config carries a secret key (blanked) only when a
			// value is stored, so key presence means "set" (SPEC §18.9).
			_, present := s.targetCfg[f.Name]
			nf.wasSet = present
		} else if v, ok := s.targetCfg[f.Name]; ok {
			nf.input.SetValue(stringifyConfigVal(v))
		}
	} else {
		nf.input.SetValue(f.Default)
	}
	return nf
}

// handleKey applies a key press: navigation, save, toggles, the kind select, or
// text entry routed to the focused input.
func (s *notificationFormScreen) handleKey(msg tea.KeyPressMsg) (Screen, tea.Cmd) {
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
	switch f.kind {
	case nfBool:
		if key.Matches(msg, formToggleKey) {
			f.on = !f.on
		}
		return s, nil
	case nfSelect:
		if key.Matches(msg, formToggleKey) && len(f.choices) > 0 {
			f.choice = (f.choice + 1) % len(f.choices)
			s.rebuildDynamic()
		}
		return s, nil
	default:
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		return s, cmd
	}
}

// moveCursor shifts the field cursor by delta, clamped to the field range.
func (s *notificationFormScreen) moveCursor(delta int) {
	s.cursor += delta
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= len(s.fields) {
		s.cursor = len(s.fields) - 1
	}
}

// syncFocus focuses the text input under the cursor and blurs the others.
func (s *notificationFormScreen) syncFocus() tea.Cmd {
	var cmd tea.Cmd
	for i, f := range s.fields {
		if f.kind != nfText {
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

// submit validates client-side and returns the create/update command, or nil
// when a field is invalid (SPEC §24.3). Blank secret fields are omitted so a
// stored secret is preserved on edit and a create simply leaves it unset
// (SPEC §18.9).
func (s *notificationFormScreen) submit() tea.Cmd {
	for _, f := range s.fields {
		f.fieldErr = ""
	}
	s.formErr = ""

	ok := true
	name := strings.TrimSpace(s.metaText("name"))
	if name == "" {
		s.setErr("name", "must not be empty")
		ok = false
	}

	cfg := map[string]any{}
	for _, f := range s.dynamic {
		if f.kind == nfBool {
			cfg[f.name] = f.on
			continue
		}
		val := strings.TrimSpace(f.input.Value())
		if val == "" {
			// A blank secret on edit keeps the stored value (SPEC §18.9), so it
			// is omitted without error. A required non-secret — or a required
			// secret on create, where nothing is stored yet — is an error.
			if f.required && (!f.secret || !s.editing) {
				s.setErr(f.name, "must not be empty")
				ok = false
			}
			continue
		}
		if f.ftype == notify.FieldTypeNumber {
			n, err := strconv.Atoi(val)
			if err != nil {
				s.setErr(f.name, "must be a whole number")
				ok = false
				continue
			}
			cfg[f.name] = n
			continue
		}
		cfg[f.name] = val
	}
	if !ok {
		return nil
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		s.formErr = "could not encode the configuration"
		return nil
	}
	enabled := s.metaBool("enabled")
	if s.editing {
		return updateNotificationTargetCmd(s.client, s.targetID, ipc.UpdateNotificationTargetRequest{
			Name: &name, Enabled: &enabled, Config: cfgJSON,
		})
	}
	return createNotificationTargetCmd(s.client, ipc.CreateNotificationTargetRequest{
		Name: name, Kind: s.metaSelect("kind"), Enabled: enabled, Config: cfgJSON,
	})
}

// applyServerError shows a server validation_error against the field it names,
// falling back to a form-level error for an unrecognised field (SPEC §10.3).
func (s *notificationFormScreen) applyServerError(apiErr *ipc.APIError) {
	for i, f := range s.fields {
		if apiErr.Field != "" && f.name == apiErr.Field {
			f.fieldErr = apiErr.Message
			s.cursor = i
			s.syncFocus()
			return
		}
	}
	s.formErr = apiErr.Message
}

// View renders the form fields with the cursor, values, secret state, and any
// validation errors.
func (s *notificationFormScreen) View() string {
	var b strings.Builder
	b.WriteString(s.Title() + "\n\n")
	if !s.loaded {
		b.WriteString("loading…")
		return b.String()
	}
	if s.editing {
		fmt.Fprintf(&b, "kind: %s (fixed)\n\n", s.target.Kind)
	}
	for i, f := range s.fields {
		cursor := "  "
		if i == s.cursor {
			cursor = "› "
		}
		fmt.Fprintf(&b, "%s%-22s ", cursor, f.label)
		switch f.kind {
		case nfBool:
			b.WriteString(checkbox(f.on))
		case nfSelect:
			fmt.Fprintf(&b, "‹ %s ›", f.choiceValue())
		default:
			b.WriteString(f.input.View())
			if f.secret {
				b.WriteString("  " + f.secretHint())
			}
		}
		if f.fieldErr != "" {
			b.WriteString("  " + formErrStyle.Render("✗ "+f.fieldErr))
		}
		b.WriteByte('\n')
	}
	if s.formErr != "" {
		b.WriteString("\n" + formErrStyle.Render(s.formErr) + "\n")
	}
	b.WriteString("\ntab/↑↓ move • space toggle/cycle • ctrl+s save • esc cancel")
	return b.String()
}

// Title is the screen name shown in the status bar.
func (s *notificationFormScreen) Title() string {
	if s.editing {
		return "Edit Target"
	}
	return "New Target"
}

// secretHint renders the set/unset state of a secret field (SPEC §18.9).
func (f *nfField) secretHint() string {
	if f.wasSet {
		return "(set — blank keeps current)"
	}
	return "(unset)"
}

// choiceValue returns the select field's current choice, or "" when empty.
func (f *nfField) choiceValue() string {
	if f.choice < 0 || f.choice >= len(f.choices) {
		return ""
	}
	return f.choices[f.choice]
}

// providerKinds lists the provider kinds for the create-mode kind select.
func (s *notificationFormScreen) providerKinds() []string {
	kinds := make([]string, 0, len(s.providers))
	for _, p := range s.providers {
		kinds = append(kinds, p.Kind)
	}
	return kinds
}

// currentProvider returns the provider whose fields the form should render: the
// target's kind when editing, or the selected kind when creating.
func (s *notificationFormScreen) currentProvider() *ipc.NotificationProviderResponse {
	kind := s.target.Kind
	if !s.editing {
		if f := s.field("kind"); f != nil {
			kind = f.choiceValue()
		}
	}
	for i := range s.providers {
		if s.providers[i].Kind == kind {
			return &s.providers[i]
		}
	}
	return nil
}

// field returns the form field with the given name, or nil.
func (s *notificationFormScreen) field(name string) *nfField {
	for _, f := range s.fields {
		if f.name == name {
			return f
		}
	}
	// Before recompose, meta rows live only in s.meta.
	for _, f := range s.meta {
		if f.name == name {
			return f
		}
	}
	return nil
}

func (s *notificationFormScreen) metaText(name string) string {
	if f := s.field(name); f != nil {
		return f.input.Value()
	}
	return ""
}

func (s *notificationFormScreen) metaBool(name string) bool {
	if f := s.field(name); f != nil {
		return f.on
	}
	return false
}

func (s *notificationFormScreen) metaSelect(name string) string {
	if f := s.field(name); f != nil {
		return f.choiceValue()
	}
	return ""
}

func (s *notificationFormScreen) setErr(name, msg string) {
	if f := s.field(name); f != nil {
		f.fieldErr = msg
	}
}

// newNFText builds a text field seeded with value.
func newNFText(name, label, value string, required bool) *nfField {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(value)
	return &nfField{name: name, label: label, kind: nfText, required: required, input: ti}
}

// parseConfigMap decodes a target's JSON config into a map for field seeding,
// returning an empty map on any failure.
func parseConfigMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) == 0 {
		return m
	}
	_ = json.Unmarshal(raw, &m)
	return m
}

// stringifyConfigVal renders a decoded JSON config value as form-input text.
func stringifyConfigVal(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// emitNotificationTargetsChanged signals the target list to re-fetch.
func emitNotificationTargetsChanged() tea.Msg { return notificationTargetsChangedMsg{} }

// fetchNotificationProvidersCmd fetches provider metadata over IPC (SPEC §19.3).
func fetchNotificationProvidersCmd(c Client) tea.Cmd {
	return ipcCmd(c.NotificationProviders,
		func(ps []ipc.NotificationProviderResponse) tea.Msg {
			return notificationProvidersLoadedMsg{providers: ps}
		})
}

// fetchNotificationTargetCmd fetches the target an edit form seeds from.
func fetchNotificationTargetCmd(c Client, id string) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) (ipc.NotificationTargetResponse, error) {
			return c.GetNotificationTarget(ctx, id)
		},
		func(t ipc.NotificationTargetResponse) tea.Msg { return notificationTargetLoadedMsg{target: t} },
	)
}

// createNotificationTargetCmd submits a new target over IPC, mapping a server
// validation_error to the form and any other failure to the error dialog.
func createNotificationTargetCmd(c Client, req ipc.CreateNotificationTargetRequest) tea.Cmd {
	return func() tea.Msg {
		_, err := c.CreateNotificationTarget(context.Background(), req)
		return notificationSaveResult(err)
	}
}

// updateNotificationTargetCmd submits a partial target update over IPC.
func updateNotificationTargetCmd(c Client, id string, req ipc.UpdateNotificationTargetRequest) tea.Cmd {
	return func() tea.Msg {
		_, err := c.UpdateNotificationTarget(context.Background(), id, req)
		return notificationSaveResult(err)
	}
}

// notificationSaveResult turns a save outcome into the right message: success,
// a field-scoped validation error, or a generic error for the dialog.
func notificationSaveResult(err error) tea.Msg {
	if err == nil {
		return notificationSavedMsg{}
	}
	var apiErr *ipc.APIError
	if errors.As(err, &apiErr) && apiErr.Code == ipc.ErrValidation {
		return notificationFormErrorMsg{apiErr: apiErr}
	}
	return errMsg{err: err}
}
