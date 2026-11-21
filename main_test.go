package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/manifest"
	"gopkg.in/yaml.v3"
)

func call(t *testing.T, send func(context.Context, Message) error, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	routes(send, testKit{}).ServeHTTP(rec, httptest.NewRequest(method, path, nil))

	return rec
}

func TestTheTestRouteSendsOneMessageNamingTheModule(t *testing.T) {
	use(t, defaults(t))

	var sent []Message

	rec := call(t, func(_ context.Context, m Message) error { sent = append(sent, m); return nil }, http.MethodPost, "/test")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if len(sent) != 1 || !strings.Contains(sent[0].Title, "Pushover Notifications") {
		t.Fatalf("sent %+v", sent)
	}
}

func TestTheTestRouteRefusesWhileUnconfigured(t *testing.T) {
	use(t, Settings{})

	rec := call(t, func(context.Context, Message) error { t.Error("sent while unconfigured"); return nil }, http.MethodPost, "/test")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "NOT_CONFIGURED") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestTheTestRouteSaysWhyPushoverRefusedWithoutTheToken(t *testing.T) {
	use(t, defaults(t))

	rec := call(t, func(context.Context, Message) error {
		return &sendError{Reason: "Pushover refused the message (HTTP 400): application token is invalid"}
	}, http.MethodPost, "/test")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "application token is invalid") || strings.Contains(body, goodToken) {
		t.Fatalf("body %s", body)
	}
}

func TestAnOtherErrorFromTheSenderIsStillABadGateway(t *testing.T) {
	use(t, defaults(t))

	rec := call(t, func(context.Context, Message) error { return errors.New("boom") }, http.MethodPost, "/test")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestAnythingElseIsOurOwnNotFound(t *testing.T) {
	rec := call(t, nil, http.MethodGet, "/nowhere")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "ROUTE_NOT_FOUND") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	rec = call(t, nil, http.MethodGet, "/test")
	if rec.Code == http.StatusOK {
		t.Fatal("GET /test sent a message")
	}
}

func TestTheManifestParsesWithTheSDK(t *testing.T) {
	document, err := os.ReadFile("module.yaml")
	if err != nil {
		t.Fatal(err)
	}

	declared, err := manifest.Parse("module.yaml", document, yaml.Unmarshal)
	if err != nil {
		t.Fatalf("module.yaml is refused: %v", err)
	}

	if declared.Name != name {
		t.Fatalf("the manifest names %q, the code reports %q", declared.Name, name)
	}
}

// Every option the manifest declares is a key the settings reader knows, and
// every secret is declared as one, so the host masks it.
func TestEveryDeclaredOptionIsReadAndTheCredentialsAreSecrets(t *testing.T) {
	document, err := os.ReadFile("module.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var file struct {
		Options []struct {
			Key     string `yaml:"key"`
			Type    string `yaml:"type"`
			Default string `yaml:"default"`
		} `yaml:"options"`
	}

	if err = yaml.Unmarshal(document, &file); err != nil {
		t.Fatal(err)
	}

	flat := map[string]string{}

	for _, option := range file.Options {
		flat[option.Key] = option.Default

		if (option.Key == "pushover_token" || option.Key == "pushover_user") && option.Type != "secret" {
			t.Errorf("%s is type %s, not secret", option.Key, option.Type)
		}
	}

	if len(flat) < 10 {
		t.Fatalf("only %d options declared", len(flat))
	}

	raw, _ := json.Marshal(flat)

	var s Settings
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the manifest's own defaults are refused: %v", err)
	}

	if err = s.validate(); err != nil {
		t.Fatalf("the manifest's own defaults do not validate: %v", err)
	}

	// The defaults in the manifest are the defaults in the code.
	for kind, want := range defaultRules() {
		if s.Rules[kind] != want {
			t.Errorf("%s: the manifest's default is %+v, the code's is %+v", kind, s.Rules[kind], want)
		}
	}
}

// Every key the settings reader accepts is declared in the manifest, and the
// manifest declares nothing the reader does not know, so a key cannot be added
// to one and forgotten in the other.
func TestTheManifestDeclaresExactlyTheKeysTheReaderAccepts(t *testing.T) {
	document, err := os.ReadFile("module.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var file struct {
		Options []struct {
			Key string `yaml:"key"`
		} `yaml:"options"`
	}

	if err = yaml.Unmarshal(document, &file); err != nil {
		t.Fatal(err)
	}

	declared := map[string]bool{}
	for _, option := range file.Options {
		declared[option.Key] = true
	}

	want := []string{"pushover_token", "pushover_user", "muted_printers", "connect_url", "connect_token", "timezone"}

	for _, kind := range kinds {
		for _, field := range []string{"notify", "priority", "sound", "title", "body", "image"} {
			want = append(want, field+"_"+kind)
		}
	}

	for slot := 1; slot <= maxCameras; slot++ {
		want = append(want, fmt.Sprintf("camera%d_printer", slot), fmt.Sprintf("camera%d_name", slot))
	}

	for _, key := range want {
		if !declared[key] {
			t.Errorf("%s is read by the module and not declared in module.yaml", key)
		}

		delete(declared, key)
	}

	for key := range declared {
		t.Errorf("%s is declared in module.yaml and the module does not read it", key)
	}
}

// The ports the settings accept are the ports the manifest lets the module
// connect to, so an address the page takes is one the host will not block.
func TestTheAcceptedPortsAreTheOnesTheManifestAllows(t *testing.T) {
	document, err := os.ReadFile("module.yaml")
	if err != nil {
		t.Fatal(err)
	}

	declared, err := manifest.Parse("module.yaml", document, yaml.Unmarshal)
	if err != nil {
		t.Fatal(err)
	}

	for _, egress := range declared.Egress {
		if egress.Option != "connect_url" {
			continue
		}

		if !slices.Equal(egress.Ports, connectPorts) {
			t.Fatalf("module.yaml lets connect_url use %v, the settings accept %v", egress.Ports, connectPorts)
		}

		return
	}

	t.Fatal("module.yaml declares no egress for connect_url")
}

func TestAKindTestSendsThatKindsMessageAndSaysWhatItDid(t *testing.T) {
	use(t, defaults(t))

	var sent []Message

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"kind":"attention"}`))
	routes(func(_ context.Context, m Message) error { sent = append(sent, m); return nil }, testKit{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || len(sent) != 1 {
		t.Fatalf("%d %s sent %d", rec.Code, rec.Body, len(sent))
	}

	if !strings.Contains(sent[0].Title, "needs you") || !strings.Contains(rec.Body.String(), `"picture":"none"`) {
		t.Fatalf("%+v %s", sent[0], rec.Body)
	}
}

func TestAKindTestRefusesAKindItDoesNotKnow(t *testing.T) {
	use(t, defaults(t))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"kind":"nope"}`))
	routes(func(context.Context, Message) error { t.Error("sent"); return nil }, testKit{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "UNKNOWN_KIND") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAKindTestWithABrokenBodyIsRefused(t *testing.T) {
	use(t, defaults(t))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{`))
	routes(func(context.Context, Message) error { t.Error("sent"); return nil }, testKit{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// The message types the test route takes are the ones the module has, so a type
// added in Go and forgotten in openapi.json fails here. The refusal for an
// unknown type lists them from the same slice.
func TestTheTestRouteTakesTheKindsTheModuleHas(t *testing.T) {
	document, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}

	var api struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Properties struct {
							Kind struct {
								Enum []string `json:"enum"`
							} `json:"kind"`
						} `json:"properties"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}

	if err = json.Unmarshal(document, &api); err != nil {
		t.Fatal(err)
	}

	got := api.Paths["/test"]["post"].RequestBody.Content["application/json"].Schema.Properties.Kind.Enum
	if !slices.Equal(got, kinds) {
		t.Fatalf("openapi.json lets /test take %v, the module has %v", got, kinds)
	}
}
