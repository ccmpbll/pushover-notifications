package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// pushoverURL is the one address this module connects to outside Prusa
// Connect. module.yaml declares it as egress.
const pushoverURL = "https://api.pushover.net/1/messages.json"

// pushoverClient follows no redirect: the credentials are in the body of the
// request, and a redirect would send them wherever it points.
//
//nolint:gochecknoglobals // one client for the process, so connections are reused.
var pushoverClient = noRedirects(&http.Client{Timeout: 10 * time.Second})

func noRedirects(c *http.Client) *http.Client {
	clone := *c
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &clone
}

type pushover struct {
	endpoint string
	client   *http.Client
}

// sendError is a message that was not sent. Temporary says whether asking
// again can work. Reason is safe to show and to log: it never holds a
// credential.
type sendError struct {
	Temporary bool
	Reason    string
}

func (e *sendError) Error() string { return "pushover: " + e.Reason }

// Send posts one message. The token and the user key are in the form, as
// Pushover's API wants them, and appear in no URL. A message with a picture is
// sent as a multipart form, with the picture as the attachment.
func (p *pushover) Send(ctx context.Context, token, user string, m Message) error {
	fields := map[string]string{
		"token":    token,
		"user":     user,
		"title":    m.Title,
		"message":  m.Body,
		"priority": strconv.Itoa(m.Priority),
	}

	if m.Sound != "" {
		fields["sound"] = m.Sound
	}

	if !m.Time.IsZero() {
		fields["timestamp"] = strconv.FormatInt(m.Time.Unix(), 10)
	}

	payload, contentType, err := encodeForm(fields, m.Image, m.ImageType)
	if err != nil {
		return &sendError{Reason: "the request could not be built"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, payload)
	if err != nil {
		return &sendError{Reason: "the request could not be built"}
	}

	req.Header.Set("Content-Type", contentType)

	resp, err := p.client.Do(req)
	if err != nil {
		reason := err.Error()

		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			reason = urlErr.Err.Error()
		}

		return &sendError{Temporary: true, Reason: "could not reach Pushover: " + scrub(reason, token, user)}
	}

	defer resp.Body.Close()

	var answer struct {
		Status int      `json:"status"`
		Errors []string `json:"errors"`
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(body, &answer)

	switch {
	case resp.StatusCode == http.StatusOK && answer.Status == 1:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return &sendError{Temporary: true, Reason: fmt.Sprintf("Pushover is not taking messages now (HTTP %d)", resp.StatusCode)}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return &sendError{Reason: fmt.Sprintf("Pushover redirected the request (HTTP %d), which is not followed", resp.StatusCode)}
	default:
		why := scrub(strings.Join(answer.Errors, "; "), token, user)
		if why == "" {
			why = "no reason given"
		}

		return &sendError{Reason: fmt.Sprintf("Pushover refused the message (HTTP %d): %s", resp.StatusCode, why)}
	}
}

// scrub takes the credentials out of text that came from outside, so a reason
// can be logged and shown.
func scrub(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "…")
		}
	}

	return text
}

// encodeForm writes the fields as a urlencoded form, or as a multipart form
// when there is a picture to attach.
func encodeForm(fields map[string]string, image []byte, imageType string) (io.Reader, string, error) {
	if len(image) == 0 {
		form := url.Values{}
		for key, value := range fields {
			form.Set(key, value)
		}

		return strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", nil
	}

	var buf bytes.Buffer

	writer := multipart.NewWriter(&buf)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", err
		}
	}

	extension := ".jpg"
	if imageType == "image/png" {
		extension = ".png"
	}

	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="attachment"; filename="picture`+extension+`"`)
	header.Set("Content-Type", imageType)

	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}

	if _, err = part.Write(image); err != nil {
		return nil, "", err
	}

	if err = writer.Close(); err != nil {
		return nil, "", err
	}

	return &buf, writer.FormDataContentType(), nil
}
