package providerhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPostJSON_PostsBodyAndContentType is the happy path: the helper sends the
// exact body, sets the JSON content type, and defaults a blank method to POST.
// Every webhook-family provider depends on these defaults, so a regression
// here breaks all of them at once.
func TestPostJSON_PostsBodyAndContentType(t *testing.T) {
	var gotMethod, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := []byte(`{"hello":"world"}`)
	if err := PostJSON(context.Background(), srv.Client(), "", srv.URL, body); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST (blank should default)", gotMethod)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}
	if string(gotBody) != string(body) {
		t.Errorf("body = %q, want %q", gotBody, body)
	}
}

// TestPostJSON_HonorsMethod proves a caller can override the verb (the generic
// webhook provider exposes a configurable method).
func TestPostJSON_HonorsMethod(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
	}))
	defer srv.Close()
	if err := PostJSON(context.Background(), srv.Client(), http.MethodPut, srv.URL, []byte(`{}`)); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
}

// TestPostJSON_Non2xxReturnsErrorWithStatus: a non-2xx response is a delivery
// failure the pipeline must retry, so it has to surface as an error. The error
// names the status (useful for operators) but must not echo the URL, which is
// a secret (SPEC §18.9, §23).
func TestPostJSON_Non2xxReturnsErrorWithStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	secretURL := srv.URL + "/services/SECRET-TOKEN"

	err := PostJSON(context.Background(), srv.Client(), "", secretURL, []byte(`{}`))
	if err == nil {
		t.Fatal("PostJSON returned nil for a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention status 500", err)
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Errorf("error leaked the secret URL: %q", err)
	}
}

// TestPostJSON_TransportErrorIsSanitized: when the connection itself fails the
// underlying error normally embeds the dialled URL/host. The helper must strip
// it so a secret webhook endpoint never lands in logs or attempt records.
func TestPostJSON_TransportErrorIsSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	secretURL := srv.URL + "/hooks/SUPER-SECRET"
	client := srv.Client()
	srv.Close() // connections are now refused

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := PostJSON(ctx, client, "", secretURL, []byte(`{}`))
	if err == nil {
		t.Fatal("PostJSON returned nil against a closed server")
	}
	if strings.Contains(err.Error(), "SUPER-SECRET") {
		t.Errorf("error leaked the secret URL: %q", err)
	}
}

// TestPostJSON_PreservesContextCancellation: the delivery pipeline cancels
// in-flight sends at shutdown and classifies timeouts for retry. The helper
// must keep context sentinels matchable with errors.Is even while redacting
// the URL.
func TestPostJSON_PreservesContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := PostJSON(ctx, srv.Client(), "", srv.URL+"/secret-path", []byte(`{}`))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want errors.Is(..., context.Canceled)", err)
	}
	if strings.Contains(err.Error(), "secret-path") {
		t.Errorf("error leaked the URL: %q", err)
	}
}
