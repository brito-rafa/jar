package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfiguredProxyOrigin(t *testing.T) {
	const public = "https://chat.example.test"
	for _, tc := range []struct {
		host, origin string
		want         int
	}{
		{"chat.example.test", "", http.StatusOK},
		{"chat.example.test", public, http.StatusOK},
		{"127.0.0.1:8080", public, http.StatusOK},
		{"localhost:8080", public, http.StatusOK},
		{"127.0.0.1:8080", "http://127.0.0.1:8080", http.StatusOK},
		{"evil.example.test", public, http.StatusForbidden},
		{"chat.example.test", "https://evil.example.test", http.StatusForbidden},
		{"chat.example.test", "http://chat.example.test", http.StatusForbidden},
		{"chat.example.test", public + ".evil.test", http.StatusForbidden},
		{"chat.example.test", public + "/path", http.StatusForbidden},
		{"chat.example.test", "null", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/api/chat", strings.NewReader(`{"message":"/help"}`))
		req.Host = tc.host
		req.Header.Set("Content-Type", "application/json")
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		webHandler(fixtureSession(), public).ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://chat.example.test/", nil)
	w := httptest.NewRecorder()
	webHandler(fixtureSession()).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("default accepted external host", w.Code)
	}
}
func TestPublicURLValidation(t *testing.T) {
	for _, value := range []string{"https://chat.example.test", "https://chat.example.test/", "http://chat.example.test:8080", ""} {
		if _, err := parsePublicURL(value); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"chat.example.test", "https://user:secret@chat.example.test", "https://chat.example.test/path", "https://chat.example.test?q=1", "https://chat.example.test#fragment", "https://*.example.test", "ftp://chat.example.test", "https://chat.example.test:0", "https://chat.example.test:99999"} {
		if _, err := parsePublicURL(value); err == nil {
			t.Fatal("invalid public URL accepted", value)
		}
	}
}

func TestOpenHostMode(t *testing.T) {
	for _, tc := range []struct {
		host, origin string
		want         int
	}{
		{"any-host.example.test", "", http.StatusOK},
		{"any-host.example.test", "https://any-host.example.test", http.StatusOK},
		{"192.0.2.10:8080", "http://192.0.2.10:8080", http.StatusOK},
		{"any-host.example.test", "https://different.example.test", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/api/chat", strings.NewReader(`{"message":"/help"}`))
		req.Host = tc.host
		req.Header.Set("Content-Type", "application/json")
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		webHandlerPolicy(fixtureSession(), "", true).ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}
