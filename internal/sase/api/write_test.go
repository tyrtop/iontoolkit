package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSendPutsTheBodyAtThePath(t *testing.T) {
	c := snapshotClient(t, routes{
		"/interfaces/17": func() (int, string) { return http.StatusOK, `{"id":"17","description":"new"}` },
	})
	var method, path string
	var sent []byte
	c.client.Transport = recorder{
		inner: c.client.Transport,
		saw: func(r *http.Request, b []byte) {
			method, path, sent = r.Method, r.URL.Path, b
		},
	}

	got, err := c.Send(context.Background(), "put", "/sdwan/v4.21/api/sites/16/elements/15/interfaces/17", []byte(`{"description":"new"}`))
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if method != http.MethodPut {
		t.Errorf("method = %s, want PUT", method)
	}
	if path != "/sdwan/v4.21/api/sites/16/elements/15/interfaces/17" {
		t.Errorf("path = %s", path)
	}
	if string(sent) != `{"description":"new"}` {
		t.Errorf("body = %s", sent)
	}
	if !strings.Contains(string(got), `"new"`) {
		t.Errorf("answer = %s", got)
	}
}

func TestSendRefusesWhatIsNotAWrite(t *testing.T) {
	c := snapshotClient(t, routes{})
	tests := []struct {
		name, method, path string
	}{
		{"get", http.MethodGet, "/sdwan/v2.0/api/elements"},
		{"another host", http.MethodPut, "//evil.example/sdwan/x"},
		{"outside sdwan", http.MethodPut, "/mt/monitor/v1/x"},
		{"walks up", http.MethodDelete, "/sdwan/../mt/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.Send(context.Background(), tt.method, tt.path, nil); err == nil {
				t.Errorf("Send(%s %s) sent it", tt.method, tt.path)
			}
		})
	}
}
