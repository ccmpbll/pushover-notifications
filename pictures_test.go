package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var jpegBytes = append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{1}, 100)...)

func connectServer(t *testing.T, handler http.HandlerFunc) (*connectAPI, Settings) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &connectAPI{client: noRedirects(srv.Client())}, Settings{ConnectURL: srv.URL, ConnectToken: "tok_secret123"}
}

func TestAThumbnailIsAskedForWithTheTokenAndTheStoragePrefixedPath(t *testing.T) {
	var gotAuth, gotPath, gotQuery string

	api, s := connectServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotQuery = r.Header.Get("Authorization"), r.URL.EscapedPath(), r.URL.RawQuery

		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(jpegBytes)
	})

	data, mediaType, err := api.Thumbnail(context.Background(), s, "dev/1", "/usb/my models/benchy.bgcode")
	if err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer tok_secret123" {
		t.Errorf("authorization %q", gotAuth)
	}

	if gotPath != "/api/v1/devices/dev%2F1/files/thumbnail" {
		t.Errorf("path %q: the device id was not escaped", gotPath)
	}

	if gotQuery != "path=usb%2Fmy+models%2Fbenchy.bgcode" {
		t.Errorf("query %q: want the path without its leading slash, escaped", gotQuery)
	}

	if mediaType != "image/png" || !bytes.Equal(data, jpegBytes) {
		t.Errorf("%s %d bytes", mediaType, len(data))
	}
}

func TestASnapshotIsAskedForByCameraName(t *testing.T) {
	var gotPath string

	api, s := connectServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()

		w.Header().Set("Content-Type", "image/jpeg; charset=binary")
		_, _ = w.Write(jpegBytes)
	})

	_, mediaType, err := api.Snapshot(context.Background(), s, "mk4s cam")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/api/v1/cameras/mk4s%20cam/snapshot" || mediaType != "image/jpeg" {
		t.Errorf("%q %q", gotPath, mediaType)
	}
}

func TestPicturesThatAreNotPictures(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"not found": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"a picture in an error": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write(jpegBytes)
		},
		"unauthorized": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"no frame yet": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		"html": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
		},
		"no content type": func(w http.ResponseWriter, _ *http.Request) {
			w.Header()["Content-Type"] = nil
			_, _ = w.Write(jpegBytes)
		},
		"too big": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(bytes.Repeat([]byte{1}, maxPicture+1))
		},
		"empty": func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Content-Type", "image/jpeg") },
	}

	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			api, s := connectServer(t, handler)

			if _, _, err := api.Snapshot(context.Background(), s, "cam"); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestAnErrorFromConnectNeverHoldsTheToken(t *testing.T) {
	api, s := connectServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"token tok_secret123 may not"}`))
	})

	_, _, err := api.Snapshot(context.Background(), s, "cam")
	if err == nil || strings.Contains(err.Error(), "tok_secret123") {
		t.Fatalf("%v", err)
	}

	dead := &connectAPI{client: noRedirects(&http.Client{})}

	_, _, err = dead.Snapshot(context.Background(), Settings{ConnectURL: "http://127.0.0.1:1", ConnectToken: "tok_secret123"}, "cam")
	if err == nil || strings.Contains(err.Error(), "tok_secret123") {
		t.Fatalf("%v", err)
	}
}

func TestARedirectIsNotFollowedSoTheTokenStaysHome(t *testing.T) {
	followed := false

	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer other.Close()

	api, s := connectServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	})

	if _, _, err := api.Snapshot(context.Background(), s, "cam"); err == nil || followed {
		t.Fatalf("err %v, followed %v", err, followed)
	}
}

func TestNoAddressOrTokenIsNoPicture(t *testing.T) {
	api := &connectAPI{client: http.DefaultClient}

	if _, _, err := api.Snapshot(context.Background(), Settings{}, "cam"); err == nil {
		t.Fatal("asked with no address")
	}
}

func TestACameraNameWithAwkwardCharactersIsEscapedIntoOneSegment(t *testing.T) {
	var gotPath string

	api, s := connectServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()

		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegBytes)
	})

	if _, _, err := api.Snapshot(context.Background(), s, "a;b,c"); err != nil {
		t.Fatal(err)
	}

	if gotPath != "/api/v1/cameras/a%3Bb%2Cc/snapshot" {
		t.Errorf("path %q", gotPath)
	}
}

func TestARefusedPictureSaysWhatToFix(t *testing.T) {
	cases := []struct {
		status    int
		thumbnail bool
		want      []string
	}{
		{http.StatusUnauthorized, false, []string{"HTTP 401", "expired"}},
		{http.StatusForbidden, true, []string{"HTTP 403", "scope operate"}},
		{http.StatusForbidden, false, []string{"HTTP 403", "scope read"}},
		{http.StatusNotFound, false, []string{"HTTP 404", "camera"}},
		{http.StatusBadGateway, false, []string{"HTTP 502"}},
	}

	for _, c := range cases {
		api, s := connectServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(c.status) })

		var err error
		if c.thumbnail {
			_, _, err = api.Thumbnail(context.Background(), s, "d", "/usb/a.bgcode")
		} else {
			_, _, err = api.Snapshot(context.Background(), s, "cam")
		}

		if err == nil {
			t.Fatalf("%d: no error", c.status)
		}

		for _, want := range c.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%d: %q does not say %q", c.status, err, want)
			}
		}

		if strings.Contains(err.Error(), s.ConnectToken) {
			t.Errorf("%d: the token is in %q", c.status, err)
		}
	}
}
