package openai

import (
	"strings"
	"testing"
)

func TestValidateBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		baseURL   string
		allowHTTP bool
		wantErr   string
	}{
		{name: "sdk default", baseURL: ""},
		{name: "https", baseURL: "https://api.example.com/v1/"},
		{name: "https with opt-in", baseURL: "https://api.example.com/v1/", allowHTTP: true},
		{name: "loopback ipv4 with opt-in", baseURL: "http://127.0.0.1:8080/v1/", allowHTTP: true},
		{name: "loopback ipv6 with opt-in", baseURL: "http://[::1]:8080/v1/", allowHTTP: true},
		{name: "localhost with opt-in", baseURL: "http://LocalHost:8080/v1/", allowHTTP: true},
		{
			name:    "loopback without opt-in",
			baseURL: "http://127.0.0.1:8080/v1/",
			wantErr: "authenticated requests require HTTPS",
		},
		{
			name:      "remote host with opt-in",
			baseURL:   "http://api.example.com/v1/",
			allowHTTP: true,
			wantErr:   "authenticated requests require HTTPS",
		},
		{
			name:      "localhost subdomain with opt-in",
			baseURL:   "http://localhost.example.com/v1/",
			allowHTTP: true,
			wantErr:   "authenticated requests require HTTPS",
		},
		{
			name:      "opaque loopback with opt-in",
			baseURL:   "http:127.0.0.1:8080",
			allowHTTP: true,
			wantErr:   "authenticated requests require HTTPS",
		},
		{
			name:      "unsupported scheme with opt-in",
			baseURL:   "ftp://127.0.0.1/v1/",
			allowHTTP: true,
			wantErr:   "authenticated requests require HTTPS",
		},
		{
			name:    "malformed url hides credentials",
			baseURL: "https://user:secret@api.example.com:port/",
			wantErr: "invalid OPENAI_BASE_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateBaseURL(tt.baseURL, tt.allowHTTP)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateBaseURL(%q, %t) = %v, want nil", tt.baseURL, tt.allowHTTP, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf(
					"validateBaseURL(%q, %t) = %v, want error containing %q",
					tt.baseURL,
					tt.allowHTTP,
					err,
					tt.wantErr,
				)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("validateBaseURL(%q, %t) error exposes credentials: %v", tt.baseURL, tt.allowHTTP, err)
			}
		})
	}
}
