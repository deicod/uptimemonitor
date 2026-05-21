package ipc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/deicod/uptimemonitor/internal/notify"
)

// TestClientNotificationProviders exercises the full HTTP round-trip over
// the Unix socket so the client decodes the same wire shape the server
// produces. The handler tests in notifications_test.go already pin the
// envelope; here we verify the typed client returns the fields verbatim,
// which is what the TUI provider form (M9.12) consumes.
func TestClientNotificationProviders(t *testing.T) {
	reg := &fakeProviderRegistry{providers: []notify.Provider{
		&fakeProvider{
			kind:        "webhook",
			displayName: "Webhook",
			fields: []notify.Field{
				{Name: "url", Type: notify.FieldTypeSecretString, Required: true, Secret: true},
				{Name: "method", Type: notify.FieldTypeString, Required: true, Default: "POST"},
			},
		},
		&fakeProvider{kind: "email", displayName: "Email"},
	}}
	sock := filepath.Join(t.TempDir(), "test.sock")
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationRegistry(reg)))
	client := NewClient(sock)

	got, err := client.NotificationProviders(context.Background())
	if err != nil {
		t.Fatalf("NotificationProviders: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("providers len = %d, want 2", len(got))
	}
	// The slice mirrors the registry's order so a TUI list won't shuffle.
	if got[0].Kind != "webhook" || got[1].Kind != "email" {
		t.Errorf("kinds = [%q,%q], want [webhook,email]", got[0].Kind, got[1].Kind)
	}
	if len(got[0].Fields) != 2 {
		t.Fatalf("webhook fields len = %d, want 2", len(got[0].Fields))
	}
	if !got[0].Fields[0].Secret || got[0].Fields[0].Type != notify.FieldTypeSecretString {
		t.Errorf("first field lost secret/type metadata: %+v", got[0].Fields[0])
	}
	if got[0].Fields[1].Default != "POST" {
		t.Errorf("default lost over the wire: %+v", got[0].Fields[1])
	}
}
