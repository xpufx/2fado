package service

import (
	"io"
	"regexp"
	"strings"
)

var (
	urlFinder = regexp.MustCompile(`https?://[^\s"'<>]+`)

	challengeQueryKeys = []string{
		"otp", "webauthn", "user_code", "device_code", "device", "code",
		"challenge", "auth", "verify", "verification", "stage", "session",
	}
)

// ExtractAuthURL scans process output for an interactive WebAuthn/2FA
// challenge URL (registry login, OAuth device flow, stage ceremony).
// It returns "" when no challenge link is present.
func ExtractAuthURL(output string) string {
	for _, raw := range urlFinder.FindAllString(output, -1) {
		u := strings.TrimRight(raw, ".,;!?)")
		if isChallengeURL(u) {
			return u
		}
	}
	return ""
}

func isChallengeURL(raw string) bool {
	lower := strings.ToLower(raw)
	var host, path, query string
	if i := strings.Index(lower, "://"); i >= 0 {
		rest := lower[i+3:]
		if j := strings.IndexAny(rest, "/?#"); j >= 0 {
			host = rest[:j]
			rest = rest[j:]
		} else {
			host = rest
			rest = ""
		}
		if j := strings.IndexAny(rest, "?#"); j >= 0 {
			path = rest[:j]
			query = rest[j+1:]
		} else {
			path = rest
		}
	}
	if strings.Contains(host, "npmjs.com") || strings.Contains(host, "npmjs.org") {
		if strings.Contains(path, "/login") || strings.Contains(path, "/stage") ||
			strings.Contains(path, "/auth") || strings.Contains(path, "/verify") ||
			strings.Contains(path, "/authorize") || hasChallengeQuery(query) {
			return true
		}
		return false
	}
	if strings.Contains(path, "/login/device") {
		return true
	}
	return hasChallengeQuery(query)
}

func hasChallengeQuery(query string) bool {
	for _, k := range challengeQueryKeys {
		if strings.Contains(query, k+"=") || strings.Contains(query, k+"&") {
			return true
		}
	}
	return false
}

// challengeScanner is an io.Writer that forwards every byte to dst while
// watching the stream for a challenge URL. The first detected URL fires
// onAuth exactly once.
func challengeScanner(dst io.Writer, onAuth func(string)) *challengeWriter {
	return &challengeWriter{dst: dst, onAuth: onAuth}
}

type challengeWriter struct {
	dst    io.Writer
	onAuth func(string)
	buf    []byte
	fired  bool
}

func (w *challengeWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	w.buf = append(w.buf, p...)
	if !w.fired {
		if u := ExtractAuthURL(string(w.buf)); u != "" {
			w.fired = true
			w.onAuth(u)
		}
		if len(w.buf) > 64*1024 {
			w.buf = w.buf[len(w.buf)-64*1024:]
		}
	}
	return n, err
}
