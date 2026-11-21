package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxPicture is the largest picture Pushover takes as an attachment.
const maxPicture = 5 << 20

// connectClient asks Prusa Connect, over its HTTP API, for the pictures a
// module has no wire call for. It follows no redirect: the token is in the
// request, and a redirect would send it wherever it points.
//
//nolint:gochecknoglobals // one client for the process, so connections are reused.
var connectClient = noRedirects(&http.Client{Timeout: 10 * time.Second})

// connectAPI reads pictures with a read-only API token.
type connectAPI struct {
	client *http.Client
}

// Thumbnail is the picture the slicer put inside a print file on a printer.
// filePath is the file as the job event gives it, and Connect wants it with the
// storage first and no leading slash.
func (c *connectAPI) Thumbnail(ctx context.Context, s Settings, deviceID, filePath string) ([]byte, string, error) {
	query := url.Values{"path": {strings.TrimPrefix(filePath, "/")}}

	return c.get(ctx, s, "/api/v1/devices/"+url.PathEscape(deviceID)+"/files/thumbnail?"+query.Encode(), "operate")
}

// Snapshot is a camera's newest still image.
func (c *connectAPI) Snapshot(ctx context.Context, s Settings, camera string) ([]byte, string, error) {
	return c.get(ctx, s, "/api/v1/cameras/"+url.PathEscape(camera)+"/snapshot", "read")
}

// get fetches one picture. scope is the API token scope this picture needs, for
// the message when Connect refuses the token.
func (c *connectAPI) get(ctx context.Context, s Settings, route, scope string) ([]byte, string, error) {
	if s.ConnectURL == "" || s.ConnectToken == "" {
		return nil, "", errors.New("connect_url and connect_token are not set")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ConnectURL+route, nil)
	if err != nil {
		return nil, "", errors.New("the request could not be built")
	}

	req.Header.Set("Authorization", "Bearer "+s.ConnectToken)
	req.Header.Set("Accept", "image/jpeg, image/png")

	resp, err := c.client.Do(req)
	if err != nil {
		reason := err.Error()

		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			reason = urlErr.Err.Error()
		}

		return nil, "", fmt.Errorf("could not reach Prusa Connect: %s", scrub(reason, s.ConnectToken))
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", refusal(resp.StatusCode, scope)
	}

	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (mediaType != "image/jpeg" && mediaType != "image/png") {
		return nil, "", errors.New("Prusa Connect answered with something that is not a JPEG or PNG")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPicture+1))
	if err != nil {
		return nil, "", errors.New("the picture could not be read")
	}

	if len(data) == 0 || len(data) > maxPicture {
		return nil, "", fmt.Errorf("the picture is empty or over %d MiB", maxPicture>>20)
	}

	return data, mediaType, nil
}

// refusal says what an answer other than 200 means for the person who has to
// fix it. Only the status and the scope the picture needs are in it, never the
// token.
func refusal(status int, scope string) error {
	switch status {
	case http.StatusUnauthorized:
		return errors.New("Prusa Connect does not accept the API token (HTTP 401). It may have expired or been deleted: make a new one under Settings, API credentials")
	case http.StatusForbidden:
		return fmt.Errorf("Prusa Connect refused the API token for this picture (HTTP 403). This picture needs a token with scope %s: check the token under Settings, API credentials", scope)
	case http.StatusNotFound:
		return errors.New("Prusa Connect has no such camera or print file (HTTP 404). Check the camera chosen for the printer")
	default:
		return fmt.Errorf("Prusa Connect answered HTTP %d, so there is no picture", status)
	}
}
