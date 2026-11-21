// Command pushover-notifications is a Prusa Connect module that is this process
// and nothing else. It follows the print events of the printers the host lets
// it observe and tells a person about them with Pushover.
//
// # The routes
//
//	POST /test   sends one message, so a person can tell the credentials work
//
// The paths are relative to the mount, so a caller reaches them under
// /api/v1/modules/com.github.aux.ccmpbll.pushover-notifications/. The host
// decides who may call them. This process checks no permission.
//
// # Settings
//
// settings.go reads them and module.yaml declares them: the Pushover
// credentials (secrets), a notify, priority and sound key for each kind of
// message, and the printers nobody is told about. Nothing here reads an
// environment variable.
//
// # Errors
//
// faults.go lists every error code of this module.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sync/atomic"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/busapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// What this child reports when the host asks it what it is. The name is the
// manifest's, and the host holds the two to each other.
const (
	name    = "pushover-notifications"
	release = extension.Version("0.1.0")
)

// held is the settings this module is running on. It is an atomic value because
// plugin.configure is answered on the wire goroutine and the routes are served
// on others.
//
//nolint:gochecknoglobals // the one piece of state this module has.
var held atomic.Pointer[Settings]

// settings is what this module is running on: what the host sent, or nothing,
// which sends nothing.
func settings() Settings {
	if current := held.Load(); current != nil {
		return *current
	}

	return Settings{}
}

// openAPI is the fragment describing the routes, with every path relative to
// the mount.
//
//go:embed openapi.json
var openAPI []byte

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}

// run writes the handshake line and then serves frames until the host closes
// the channel.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	line, err := wire.Handshake{
		Protocol: wire.ProtocolMajor,
		MinMinor: wire.MinMinor,
		MaxMinor: wire.MaxMinor,
		Address:  wire.StdioAddress,
	}.Line()
	if err != nil {
		return fmt.Errorf("render the handshake: %w", err)
	}

	if _, err = os.Stdout.WriteString(line); err != nil {
		return fmt.Errorf("write the handshake: %w", err)
	}

	sender := &pushover{endpoint: pushoverURL, client: pushoverClient}
	send := func(ctx context.Context, m Message) error {
		current := settings()
		if !current.configured() {
			return errors.New("pushover is not set up")
		}

		return sender.Send(ctx, current.Token, current.User, m)
	}

	serves := wirerpc.Methods{}

	conn, err := wirerpc.NewConn(stdio{}, wirerpc.Options{
		Role:    wirerpc.RoleChild,
		Methods: serves,
	})
	if err != nil {
		return fmt.Errorf("build the session: %w", err)
	}

	devices := deviceapi.NewClient(conn)
	pictures := &connectAPI{client: connectClient}

	// What the per-type tests reach: the printers the host knows, the print each
	// last had, and the two kinds of picture.
	kit := testKit{
		printers: func(ctx context.Context) ([]testPrinter, error) {
			page, err := devices.List(ctx, deviceapi.ListParams{Limit: 50})
			if err != nil {
				return nil, err
			}

			out := make([]testPrinter, 0, len(page.Items))
			for _, d := range page.Items {
				out = append(out, testPrinter{ID: d.ID, Name: d.Name, Serial: d.Serial, Away: !d.Connected})
			}

			return out, nil
		},
		lastPrint: func(ctx context.Context, id string) (testPrint, bool) {
			jobs, err := devices.Jobs(ctx, deviceapi.JobsParams{ID: id})
			if err != nil {
				return testPrint{}, false
			}

			return lastPrintOf(jobs)
		},
		thumbnail: pictures.Thumbnail,
		snapshot:  pictures.Snapshot,
	}

	maps.Copy(serves, methods(send, kit))

	// The pid is here because the host's log is the only place a person sees
	// this process. Nothing on the wire carries it.
	fmt.Fprintf(os.Stderr, "%s %s is serving on stdio as pid %d\n", name, release, os.Getpid())

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	box := newOutbox(send, log)

	events := &follower{
		log: log,
		subscribe: func(ctx context.Context, p busapi.SubscribeParams, on func(busapi.Window) error) error {
			return busapi.Subscribe(ctx, conn, p, on)
		},
		lookup: func(ctx context.Context, id string) (string, string, error) {
			device, err := devices.Get(ctx, deviceapi.GetParams{ID: id})

			return device.Name, device.Serial, err
		},
		put: box.Put,
		// What the printer is printing, from the host's record: it answers for a
		// printer that has gone off.
		current: func(ctx context.Context, id string) (string, float64, bool) {
			jobs, err := devices.Jobs(ctx, deviceapi.JobsParams{ID: id})
			if err != nil || jobs.Current == nil {
				return "", 0, false
			}

			return jobs.Current.DisplayName, jobs.Current.Progress, true
		},
		// The power the printer's plugs used, from the Tasmota Plugs module when
		// it is installed.
		energy: plugPower{call: routesapi.NewClient(conn).Call}.Read,
		// Pictures come from Prusa Connect's own HTTP API, with a read-only token.
		thumbnail: pictures.Thumbnail,
		snapshot:  pictures.Snapshot,
	}

	go box.Run(ctx)
	go events.Run(ctx)

	if err = conn.Serve(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}

// methods is the lifecycle set every child answers, plus the one call that
// carries this module's own HTTP surface.
func methods(send func(context.Context, Message) error, kit testKit) wirerpc.Methods {
	return wirerpc.Methods{
		wirerpc.MethodInfo: func(context.Context, wirerpc.Call) (any, error) {
			return routesapi.Describe(
				wirerpc.Info{
					Name:     name,
					Version:  release,
					Contract: wirerpc.Contract,
					// Naming the call is what asks the host for the settings. A
					// module that names none is never configured.
					Serves: []wirerpc.Method{wirerpc.MethodConfigure},
				}, openAPI)
		},
		// The settings the host merged from module.yaml's defaults and this
		// instance's config block, sent once before the first request.
		wirerpc.MethodConfigure: wirerpc.Configure(func(_ context.Context, sent Settings) error {
			if err := sent.validate(); err != nil {
				return err
			}

			if !sent.configured() {
				fmt.Fprintf(os.Stderr, "%s: pushover_token and pushover_user are not set, so no message is sent\n", name)
			}

			held.Store(&sent)

			return nil
		}),
		wirerpc.MethodShutdown: func(context.Context, wirerpc.Call) (any, error) {
			fmt.Fprintf(os.Stderr, "%s was asked to stop\n", name)

			return nil, nil
		},
		routesapi.MethodServe: routesapi.Serve(routes(send, kit)),
	}
}

// routes is the surface this module serves. The paths carry no prefix: /test
// here is /api/v1/modules/com.github.aux.ccmpbll.pushover-notifications/test to
// a caller.
func routes(send func(context.Context, Message) error, kit testKit) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /test", func(w http.ResponseWriter, r *http.Request) {
		if !settings().configured() {
			refuse(w, r, NotConfigured.New("pushover_token and pushover_user are not set"))

			return
		}

		// The body is optional. Without a kind this is the plain account test.
		var ask struct {
			Kind string `json:"kind"`
		}

		if r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
			if err == nil && len(bytes.TrimSpace(body)) > 0 {
				err = json.Unmarshal(body, &ask)
			}

			if err != nil {
				refuse(w, r, BadRequest.New("the request body is not the JSON this route takes"))

				return
			}
		}

		message := Message{
			Title: "Pushover Notifications: test",
			Body:  "If you can read this, notifications from Prusa Connect work.",
			Time:  time.Now(),
		}
		report := testReport{Picture: imageNone}

		s := settings()

		if ask.Kind != "" {
			if !slices.Contains(kinds, ask.Kind) {
				refuse(w, r, UnknownKind.New("no message of type "+ask.Kind).With(fault.StringParam("kind", ask.Kind)))

				return
			}
		}

		// A picture is fetched, and the fetch has its own time limit, so the
		// whole test is given longer than the plain one.
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()

		if ask.Kind != "" {
			message, report = kit.compose(ctx, s, ask.Kind, time.Now())
		}

		if err := send(ctx, message); err != nil {
			reason := "the message could not be sent"
			if failed, ok := errors.AsType[*sendError](err); ok {
				reason = failed.Reason
			}

			refuse(w, r, SendFailed.New("Pushover did not take the test message").With(fault.StringParam("reason", reason)))

			return
		}

		answer(w, http.StatusOK, map[string]any{
			"sent":            true,
			"printer":         report.Printer,
			"picture":         report.Picture,
			"picture_problem": report.Problem,
		})
	})

	// Everything else is this module's own refusal. It carries the code from
	// faults.go and names the path.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		refuse(w, r, RouteNotFound.
			New("this module serves no route at "+r.URL.Path).
			With(fault.StringParam("path", r.URL.Path)))
	})

	return mux
}

// answer writes one JSON body.
func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(body)
}

// stdio is the pair the host handed over. Close shuts the reading half, which
// is what ends a session whose host has gone quiet.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
