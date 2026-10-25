package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppUpdateVersions(t *testing.T) {
	for _, tc := range []struct {
		name, current, latest string
		draft, pre, want      bool
	}{
		{"new patch", "v0.1.0-alpha", "v0.1.1-alpha", false, false, true},
		{"without prefix", "0.1.0-alpha", "v0.1.1-alpha", false, false, true},
		{"numeric comparison", "v0.9.0", "v0.10.0", false, false, true},
		{"stable after alpha", "v0.1.1-alpha", "v0.1.1", false, false, true},
		{"equal", "v0.1.1-alpha", "v0.1.1-alpha", false, false, false},
		{"older", "v0.1.1-alpha", "v0.1.0-alpha", false, false, false},
		{"draft", "v0.1.0", "v0.2.0", true, false, false},
		{"prerelease flag", "v0.1.0", "v0.2.0-alpha", false, true, false},
		{"invalid tag", "v0.1.0", "latest", false, false, false},
		{"development", "development", "v0.2.0", false, false, false},
		{"commit build", "0.0.0-dev.abcdef12", "v0.2.0", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := appUpdateClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("User-Agent") != "andr36oid-sdflasher" {
					t.Error("missing user agent")
				}
				_ = json.NewEncoder(w).Encode(release{Tag: tc.latest, Draft: tc.draft, Pre: tc.pre})
			}))
			u, err := checkAppUpdate(context.Background(), client, "https://example.test/releases/latest", tc.current)
			if err != nil || (u != nil) != tc.want {
				t.Fatalf("update=%+v err=%v", u, err)
			}
		})
	}
}

func TestAppUpdateDownloadAndFailures(t *testing.T) {
	const apk = "https://github.com/andr36oid/andr36oid-sdflasher/releases/download/v0.2.0/andr36oid-sdflasher-v0.2.0-android-universal.apk"
	for _, link := range []string{apk, "https://example.com/app.apk", "", apk + "?redirect=elsewhere"} {
		client := appUpdateClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(release{Tag: "v0.2.0", Assets: []Asset{{Name: "andr36oid-sdflasher-v0.2.0-android-universal.apk", Size: 100, URL: link}}})
		}))
		u, err := checkAppUpdate(context.Background(), client, "https://example.test/releases/latest", "v0.1.0")
		if err != nil || u == nil || (u.APK != "") != (link == apk) {
			t.Fatalf("link=%q update=%+v err=%v", link, u, err)
		}
	}
	for _, status := range []int{404, 403, 500} {
		client := appUpdateClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		u, err := checkAppUpdate(context.Background(), client, "https://example.test/releases/latest", "v0.1.0")
		if u != nil || (err != nil) != (status != 404) {
			t.Fatalf("status=%d update=%+v err=%v", status, u, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if u, err := checkAppUpdate(ctx, http.DefaultClient, "https://api.github.com/", "v0.1.0"); u != nil || err == nil {
		t.Fatal("cancelled request must not produce an update")
	}
}

// Use an in-memory HTTP handler so these parsing tests do not need a listening
// socket. Requests still pass through http.Client, including their headers.
type appUpdateTransport struct{ handler http.Handler }

func (t appUpdateTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return w.Result(), nil
}
func appUpdateClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: appUpdateTransport{handler: handler}}
}
