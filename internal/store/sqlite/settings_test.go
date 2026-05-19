package sqlite

import (
	"context"
	"errors"
	"testing"
)

// TestSettingsRepoSetGet verifies a value written under a key reads back
// byte-for-byte. The settings table backs global toggles such as
// notifications-enabled, so a corrupted round-trip would silently change
// service behaviour.
func TestSettingsRepoSetGet(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewSettingsRepo(store)

	want := []byte(`{"enabled":true}`)
	if err := repo.Set(ctx, "notifications", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := repo.Get(ctx, "notifications")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Get = %s, want %s", got, want)
	}
}

// TestSettingsRepoGetMissing verifies an unset key is reported as ErrNotFound
// rather than an empty value, so callers can distinguish "never configured"
// from "configured off".
func TestSettingsRepoGetMissing(t *testing.T) {
	store := openMigrated(t)
	repo := NewSettingsRepo(store)

	if _, err := repo.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing key: err = %v, want ErrNotFound", err)
	}
}

// TestSettingsRepoSetOverwrite verifies a second Set on the same key replaces
// the value instead of failing on the primary-key conflict.
func TestSettingsRepoSetOverwrite(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewSettingsRepo(store)

	if err := repo.Set(ctx, "notifications", []byte(`{"enabled":true}`)); err != nil {
		t.Fatalf("Set first: %v", err)
	}
	if err := repo.Set(ctx, "notifications", []byte(`{"enabled":false}`)); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}

	got, err := repo.Get(ctx, "notifications")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != `{"enabled":false}` {
		t.Errorf("Get = %s, want overwritten value", got)
	}
}
