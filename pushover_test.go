package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serve(t *testing.T, handler http.HandlerFunc) *pushover {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &pushover{endpoint: srv.URL, client: noRedirects(srv.Client())}
}

func TestSendPostsTheFormPushoverExpects(t *testing.T) {
	var got url.Values

	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}

		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}

		got = r.PostForm

		_, _ = w.Write([]byte(`{"status":1,"request":"abc"}`))
	})

	err := p.Send(context.Background(), goodToken, goodUser, Message{
		Title: "T", Body: "B", Priority: 1, Sound: "siren", Time: time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"token": goodToken, "user": goodUser, "title": "T", "message": "B",
		"priority": "1", "sound": "siren", "timestamp": "1700000000",
	}

	for key, value := range want {
		if got.Get(key) != value {
			t.Errorf("%s = %q, want %q", key, got.Get(key), value)
		}
	}
}

func TestSendLeavesOutAnEmptySoundAndAZeroTime(t *testing.T) {
	var got url.Values

	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm

		_, _ = w.Write([]byte(`{"status":1}`))
	})

	if err := p.Send(context.Background(), goodToken, goodUser, Message{Title: "T", Body: "B"}); err != nil {
		t.Fatal(err)
	}

	if _, ok := got["sound"]; ok {
		t.Error("an empty sound was sent, which would override the person's own choice")
	}

	if _, ok := got["timestamp"]; ok {
		t.Error("a zero time was sent")
	}
}

func TestARefusalIsPermanentAndSaysWhyWithoutTheToken(t *testing.T) {
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)

		_, _ = w.Write([]byte(`{"status":0,"errors":["user identifier is invalid"],"token":"` + goodToken + `"}`))
	})

	err := p.Send(context.Background(), goodToken, goodUser, Message{Title: "T", Body: "B"})
	if err == nil {
		t.Fatal("a 400 was accepted")
	}

	var failed *sendError
	if !errors.As(err, &failed) || failed.Temporary {
		t.Fatalf("%v is not a permanent send error", err)
	}

	if !strings.Contains(err.Error(), "user identifier is invalid") {
		t.Errorf("the reason is lost: %v", err)
	}

	if strings.Contains(err.Error(), goodToken) || strings.Contains(err.Error(), goodUser) {
		t.Errorf("the error carries a credential: %v", err)
	}
}

func TestAServerFaultAndRateLimitAndNetworkFailureAreWorthRetrying(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusTooManyRequests} {
		p := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })

		var failed *sendError

		err := p.Send(context.Background(), goodToken, goodUser, Message{Title: "T", Body: "B"})
		if !errors.As(err, &failed) || !failed.Temporary {
			t.Errorf("status %d: %v, want a temporary error", status, err)
		}
	}

	dead := &pushover{endpoint: "http://127.0.0.1:1", client: &http.Client{Timeout: time.Second}}

	var failed *sendError

	err := dead.Send(context.Background(), goodToken, goodUser, Message{Title: "T", Body: "B"})
	if !errors.As(err, &failed) || !failed.Temporary {
		t.Errorf("a refused connection: %v, want a temporary error", err)
	}
}

func TestA200WithStatusZeroIsNotASuccess(t *testing.T) {
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":0}`)) })

	if err := p.Send(context.Background(), goodToken, goodUser, Message{Title: "T", Body: "B"}); err == nil {
		t.Fatal("status 0 was accepted")
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	var followed atomic.Bool

	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer other.Close()

	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	})

	if err := p.Send(context.Background(), goodToken, goodUser, Message{Title: "T", Body: "B"}); err == nil {
		t.Error("a redirect was accepted as success")
	}

	if followed.Load() {
		t.Error("the redirect was followed, so the credentials went to another host")
	}
}

func TestAReasonThatEchoesTheCredentialsIsScrubbed(t *testing.T) {
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)

		_, _ = w.Write([]byte(`{"status":0,"errors":["token ` + goodToken + ` is invalid","user ` + goodUser + ` is invalid"]}`))
	})

	err := p.Send(context.Background(), goodToken, goodUser, Message{Title: "T", Body: "B"})
	if err == nil {
		t.Fatal("a 400 was accepted")
	}

	if strings.Contains(err.Error(), goodToken) || strings.Contains(err.Error(), goodUser) {
		t.Fatalf("the error echoes a credential: %v", err)
	}

	if !strings.Contains(err.Error(), "is invalid") {
		t.Errorf("the rest of the reason is lost: %v", err)
	}
}

func TestAPictureIsSentAsAnAttachment(t *testing.T) {
	var (
		fields map[string][]string
		file   []byte
		header textproto.MIMEHeader
	)

	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Error(err)
		}

		fields = r.MultipartForm.Value

		part, h, err := r.FormFile("attachment")
		if err != nil {
			t.Error(err)

			return
		}

		header = h.Header
		file, _ = io.ReadAll(part)

		_, _ = w.Write([]byte(`{"status":1}`))
	})

	picture := []byte{0xff, 0xd8, 0xff, 1, 2, 3}

	err := p.Send(context.Background(), goodToken, goodUser, Message{
		Title: "T", Body: "B", Priority: 1, Sound: "siren", Time: time.Unix(1700000000, 0),
		Image: picture, ImageType: "image/jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]string{"token": goodToken, "user": goodUser, "title": "T", "message": "B", "priority": "1", "sound": "siren", "timestamp": "1700000000"} {
		if got := fields[key]; len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}

	if !bytes.Equal(file, picture) || header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("attachment %v as %q", file, header.Get("Content-Type"))
	}
}

func TestAMessageWithNoPictureIsStillAPlainForm(t *testing.T) {
	var contentType string

	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")

		_, _ = w.Write([]byte(`{"status":1}`))
	})

	if err := p.Send(context.Background(), goodToken, goodUser, Message{Title: "T", Body: "B"}); err != nil {
		t.Fatal(err)
	}

	if contentType != "application/x-www-form-urlencoded" {
		t.Errorf("content type %q", contentType)
	}
}
