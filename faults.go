// The codes this module refuses a caller with.
//
// A refusal a person or a client can see is a registered code, never a bare
// English sentence. The code is what a browser half switches on to choose a
// translation, and the English sentence travels beside it so a log line and an
// untranslated screen still read.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// faultDomain is the first half of every code below. It is this module's
// namespace, which the host derived from the Go module path
// github.com/ccmpbll/pushover-notifications.
//
//nolint:gochecknoglobals // a domain built once at start-up; a const cannot hold one.
var faultDomain = mustDomain("com.github.aux.ccmpbll.pushover-notifications")

// The codes this module raises. Add a code here, and nowhere else.
//
//nolint:gochecknoglobals // definitions built once at start-up.
var (
	// RouteNotFound is a path this module does not serve.
	RouteNotFound = fault.Register(faultDomain, "ROUTE_NOT_FOUND", http.StatusNotFound, fault.Document(
		"This module does not serve that path",
		"Read openapi.json. Every path in it is relative to the mount the operator gave this module.",
		fault.RetryNever,
		fault.ShowDetail,
		"path",
	))

	// NotConfigured is a test message asked for before the credentials are set.
	NotConfigured = fault.Register(faultDomain, "NOT_CONFIGURED", http.StatusConflict, fault.Document(
		"Pushover is not set up",
		"Set pushover_token and pushover_user in this module's settings, then try again.",
		fault.RetryNever,
		fault.ShowDetail,
	))

	// BadRequest is a request body this module cannot read.
	BadRequest = fault.Register(faultDomain, "BAD_REQUEST", http.StatusBadRequest, fault.Document(
		"This module cannot read that request",
		"Send the JSON body openapi.json describes, or none.",
		fault.RetryNever,
		fault.ShowDetail,
	))

	// UnknownKind is a test asked for a message type this module does not have.
	UnknownKind = fault.Register(faultDomain, "UNKNOWN_KIND", http.StatusBadRequest, fault.Document(
		"This module has no message of that type",
		"Use one of "+strings.Join(kinds, ", ")+".",
		fault.RetryNever,
		fault.ShowDetail,
		"kind",
	))

	// SendFailed is a message Pushover did not take.
	SendFailed = fault.Register(faultDomain, "SEND_FAILED", http.StatusBadGateway, fault.Document(
		"Pushover did not take the message",
		"Read the reason. A refusal usually means a wrong token or user key. Otherwise try again in a moment.",
		fault.RetryAllowed,
		fault.ShowDetail,
		"reason",
	))
)

// mustDomain builds this module's fault domain. A name the sdk refuses is a
// mistake in this file, so it stops the process at start-up.
func mustDomain(name string) fault.Domain {
	domain, err := fault.NewDomain(name)
	if err != nil {
		panic(fmt.Sprintf("pushover-notifications: %s is not a fault domain: %v", name, err))
	}

	return domain
}

// refuse writes one refusal as RFC 9457 frames it.
func refuse(w http.ResponseWriter, r *http.Request, raised *fault.Fault) {
	problem := raised.ProblemJSON(r.URL.Path)

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)

	_ = json.NewEncoder(w).Encode(problem)
}
