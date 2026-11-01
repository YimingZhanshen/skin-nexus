package adminapi

// errors.go is the one place the management surface writes a failure.
//
// The envelope is `{error, code, message}`, identical to the player API so a
// frontend has one translator for both: `code` is stable API surface a language
// pack can render, `message` is the fallback for a client that does not know the
// code, and `error` stays the HTTP status text until every call site carries a
// code (testdata/error-budget.txt), after which it becomes the code.
//
// Two helpers answer with an error value, and which one is used is the review
// question this file exists to ask:
//
//	writeInternal / writeUpstream — a driver, a socket, a decode failure. The
//	    text belongs in the operator's log, never in the response body.
//	writeDomain — a reason this repository authored for the caller ("signature
//	    does not match", "consent required for these capabilities"). The panel
//	    shows it, so it stays — but it is named, so a leak cannot hide behind it.

import (
	"log"
	"net/http"

	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/i18n"
)

// writeErrCode answers a failure the panel can name. Codes come from
// internal/i18n (or the account service) — a literal here would be invisible to
// every dictionary.
func writeErrCode(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, map[string]string{
		"error": http.StatusText(status), "message": msg, "code": code,
	})
}

// writeErrBody answers with an envelope plus extra keys, for the failures whose
// payload is structured (an install plan, a package inspection). It takes the
// same declared-domain error as writeDomain, so a reason the operator acts on
// stays visible while a raw one still has to go through the log.
func writeErrBody(w http.ResponseWriter, status int, code string, err error, extra map[string]any) {
	body := map[string]any{
		"error": http.StatusText(status), "message": err.Error(), "code": code,
	}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

// writeDomain answers with a message this repository wrote for the caller.
func writeDomain(w http.ResponseWriter, status int, err error, code string) {
	writeErrCode(w, status, err.Error(), code)
}

// writeAccountErr answers a character or texture failure through the mapping the
// account service owns, so a code cannot mean 409 on the player surface and 500
// here.
func writeAccountErr(w http.ResponseWriter, err *account.Error) {
	writeJSON(w, err.Status(), err.Body())
}

// writeInternal is a storage or dependency failure: 500, one code, and the real
// error in the log line that names the route.
func writeInternal(r *http.Request, w http.ResponseWriter, err error) {
	writeLogged(r, w, http.StatusInternalServerError, i18n.MsgInternal, "operation failed", err)
}

// writeLogged is the same discipline where the status and code are specific —
// an upstream hub at 502, a disabled module at 503. The error goes to the log;
// the body says only what a client can act on.
func writeLogged(r *http.Request, w http.ResponseWriter, status int, code, msg string, err error) {
	log.Printf("admin %s %s: %v", r.Method, r.URL.Path, err)
	writeErrCode(w, status, msg, code)
}
