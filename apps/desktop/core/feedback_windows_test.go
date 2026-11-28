package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeedbackCommand(t *testing.T) {
	var got map[string]string
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Error("unexpected request")
		}
		got = nil
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer server.Close()
	old := feedbackURL
	feedbackURL = server.URL
	defer func() { feedbackURL = old }()

	line := `{"action":"feedback","version":"2.2.1","contact":" QQ 1 ","body":"连不上\r\n第二行"}`
	if code := feedbackCommand([]byte(line)); code != "" {
		t.Fatalf("feedback returned %q", code)
	}
	if got["app"] != "windows" || got["version"] != "2.2.1" || got["contact"] != "QQ 1" ||
		got["body"] != "连不上\n第二行" || !strings.HasPrefix(got["system"], "Windows ") || len(got) != 5 {
		t.Fatalf("unexpected payload %v", got)
	}
	status = http.StatusTooManyRequests
	if code := feedbackCommand([]byte(line)); code != "feedback_busy" {
		t.Fatalf("rate limit returned %q", code)
	}
	status = http.StatusBadGateway
	if code := feedbackCommand([]byte(line)); code != "feedback_failed" {
		t.Fatalf("server error returned %q", code)
	}
	for _, bad := range []string{
		`{"version":"2.2.1","body":"  "}`,
		`{"version":"2.2.1","body":"` + strings.Repeat("长", 2001) + `"}`,
		`{"version":"2.2.1","contact":"a\nb","body":"hi"}`,
		`{"version":"x","body":"hi"}`,
	} {
		if code := feedbackCommand([]byte(bad)); code != "invalid_request" {
			t.Fatalf("%s returned %q", bad, code)
		}
	}
}
