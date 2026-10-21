package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagementPlayerAndHubBoundaries(t *testing.T) {
	module := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })
	hub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	for _, path := range []string{"/api/v1/accounts", "/api/v1/accounts/a/currency-adjustments", "/api/v1/activities", "/console/index.html", "/api/auth/login"} {
		w := httptest.NewRecorder()
		privateHandler(hub, module).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 201 {
			t.Fatal(path, w.Code)
		}
		w = httptest.NewRecorder()
		handler(config{Upstream: "http://127.0.0.1:1"}).ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 404 {
			t.Fatal("public management exposure", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	privateHandler(hub, module).ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/announcements", nil))
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	if _, err := bootstrap(context.Background(), "0.0.0.0:0", "127.0.0.1:1"); err == nil {
		t.Fatal("wildcard bootstrap accepted")
	}
}
