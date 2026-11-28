package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// Feedback goes only to this address, and only when the user presses send.
var feedbackURL = "https://liuguang.club/api/feedback"

var feedbackVersion = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,4}){0,3}$`)

func feedbackText(s string, max int, multiline bool) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t')) {
			return "", false
		}
	}
	return s, true
}

func windowsLabel() string {
	v := windows.RtlGetVersion()
	name := "Windows 10"
	if v.MajorVersion == 10 && v.BuildNumber >= 22000 {
		name = "Windows 11"
	} else if v.MajorVersion != 10 {
		name = fmt.Sprintf("Windows %d.%d", v.MajorVersion, v.MinorVersion)
	}
	return fmt.Sprintf("%s (%d)", name, v.BuildNumber)
}

func feedbackCommand(line []byte) string {
	var req struct {
		Body, Contact, Version string
	}
	if json.Unmarshal(line, &req) != nil || !feedbackVersion.MatchString(req.Version) {
		return "invalid_request"
	}
	body, okBody := feedbackText(req.Body, 2000, true)
	contact, okContact := feedbackText(req.Contact, 100, false)
	if !okBody || body == "" || !okContact {
		return "invalid_request"
	}
	payload, _ := json.Marshal(map[string]string{
		"app": "windows", "version": req.Version, "system": windowsLabel(), "contact": contact, "body": body,
	})
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Post(feedbackURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "feedback_failed"
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK:
		fmt.Println("sent")
		return ""
	case http.StatusTooManyRequests:
		return "feedback_busy"
	default:
		return "feedback_failed"
	}
}
