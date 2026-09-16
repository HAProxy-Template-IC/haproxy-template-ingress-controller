package httpstore

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// RedactURL removes user information, queries, and fragments from diagnostics.
func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[invalid URL]"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

var diagnosticURL = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
var quotedDiagnosticURL = regexp.MustCompile(`(?i)"https?://(?:\\.|[^"\\])*"`)

// redactURLError preserves error inspection while redacting URLs in its message.
func redactURLError(err error, sourceURL string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if sourceURL != "" {
		message = strings.ReplaceAll(message, strconv.Quote(sourceURL), strconv.Quote(RedactURL(sourceURL)))
		message = strings.ReplaceAll(message, sourceURL, RedactURL(sourceURL))
	}
	message = quotedDiagnosticURL.ReplaceAllStringFunc(message, redactQuotedURL)
	message = diagnosticURL.ReplaceAllStringFunc(message, RedactURL)
	return redactedURLError{cause: err, message: message}
}

func redactQuotedURL(quoted string) string {
	raw, err := strconv.Unquote(quoted)
	if err != nil {
		return strconv.Quote("[invalid URL]")
	}
	return strconv.Quote(RedactURL(raw))
}

type redactedURLError struct {
	cause   error
	message string
}

func (e redactedURLError) Error() string { return e.message }

func (e redactedURLError) Unwrap() error { return e.cause }
