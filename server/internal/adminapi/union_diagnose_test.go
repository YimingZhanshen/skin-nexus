package adminapi

// The diagnose ops route is a pass-through, and pass-throughs are the seam
// where last round's "written but never wired" bugs lived: the union package
// can compute a cause perfectly and the panel still shows nothing if this
// handler forgets to relay it. These tests pin the relay, not the computation.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"skin-nexus/server/internal/union"
)

func diagnoseBackend(rep *union.DiagnoseReport, err error) *Backend {
	return &Backend{union: UnionDeps{
		Enabled: func() bool { return true },
		Status: func() map[string]any {
			return map[string]any{
				"key_fingerprint":     "SHA256:abcd",
				"private_key_version": 7,
				"server_list_version": 781,
			}
		},
		Diagnose: func(context.Context) (*union.DiagnoseReport, error) { return rep, err },
	}}
}

func callDiagnose(t *testing.T, b *Backend) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	b.handleUnionDiagnose(rec, httptest.NewRequest(http.MethodPost, "/api/admin/v1/union/ops/diagnose", nil))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not a JSON object: %v (%s)", err, rec.Body)
	}
	return rec.Code, out
}

func TestUnionDiagnoseRelaysCause(t *testing.T) {
	b := diagnoseBackend(&union.DiagnoseReport{
		Status: "error",
		Data:   json.RawMessage(`{"status_code":403,"headers":{"Server":["nginx"]},"body":"denied"}`),
		Cause:  "http_key",
	}, nil)
	code, out := callDiagnose(t, b)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a failed leg is still a report)", code)
	}
	if out["cause"] != "http_key" {
		t.Errorf("cause = %v, want http_key — the panel labels this token, so dropping it here silently degrades every failure to prose", out["cause"])
	}
	if out["status"] != "error" {
		t.Errorf("status = %v, want error", out["status"])
	}
	// data must arrive as the object the frontend iterates, not a string.
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %T, want object", out["data"])
	}
	if sc, _ := data["status_code"].(float64); int(sc) != 403 {
		t.Errorf("data.status_code = %v, want 403", data["status_code"])
	}
	// The status-card metadata the exported report header uses stays put.
	if out["key_fingerprint"] != "SHA256:abcd" {
		t.Errorf("key_fingerprint = %v, want SHA256:abcd", out["key_fingerprint"])
	}
}

func TestUnionDiagnoseOmitsCauseOnSuccess(t *testing.T) {
	b := diagnoseBackend(&union.DiagnoseReport{
		Status: "ok",
		Data:   json.RawMessage(`{"healthy":true,"delay":0.003}`),
	}, nil)
	_, out := callDiagnose(t, b)
	if c, present := out["cause"]; present && c != "" {
		t.Errorf("cause = %v on a good leg, want absent or empty", c)
	}
}

// The probe is the same kind of seam as the cause: computed inside union,
// useless if this handler forgets to hand it over.
func TestUnionDiagnoseRelaysProbe(t *testing.T) {
	b := diagnoseBackend(&union.DiagnoseReport{
		Status: "ok",
		Data:   json.RawMessage(`{"healthy":true}`),
		Probe: &union.DiagnoseProbe{
			TLS: &union.TLSSnapshot{Version: "TLS 1.3", Cipher: "TLS_AES_256_GCM_SHA384", CertDaysLeft: 41},
			Clock: &union.ClockEvidence{
				HubClock: "Tue, 10 Nov 2026 00:00:00 GMT", OffsetSeconds: 120, WithinWindow: false,
			},
		},
	}, nil)
	_, out := callDiagnose(t, b)
	probe, ok := out["probe"].(map[string]any)
	if !ok {
		t.Fatalf("probe = %T, want object — the panel's TLS and clock rows come only from here", out["probe"])
	}
	tlsSnap, ok := probe["tls"].(map[string]any)
	if !ok {
		t.Fatalf("probe.tls = %T, want object", probe["tls"])
	}
	if tlsSnap["version"] != "TLS 1.3" || tlsSnap["cert_days_left"] != float64(41) {
		t.Errorf("probe.tls = %v, want the version and days-left the union package reported", tlsSnap)
	}
	clock, ok := probe["clock"].(map[string]any)
	if !ok {
		t.Fatalf("probe.clock = %T, want object", probe["clock"])
	}
	if clock["within_window"] != false {
		t.Error("probe.clock.within_window lost — a hub outside the signature window would read as in step")
	}
}

// An absent probe must stay absent rather than become an empty object: the
// panel distinguishes "no connection, nothing to say" from "a connection with
// no TLS".
func TestUnionDiagnoseOmitsAbsentProbe(t *testing.T) {
	b := diagnoseBackend(&union.DiagnoseReport{
		Status: "error",
		Data:   json.RawMessage(`{"exception":"dial tcp: no route to host"}`),
		Cause:  "tcp",
	}, nil)
	_, out := callDiagnose(t, b)
	if p, present := out["probe"]; present && p != nil {
		t.Errorf("probe = %v on a leg that never connected, want null", p)
	}
}

// When the module itself refuses (union disabled), the handler still has to
// answer in the report shape — the frontend renders one table for every
// outcome, and a bare 502 with prose would read as a broken panel.
func TestUnionDiagnoseErrorPathKeepsReportShape(t *testing.T) {
	b := diagnoseBackend(nil, union.ErrDisabled)
	code, out := callDiagnose(t, b)
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", code)
	}
	if out["status"] != "error" {
		t.Errorf("status = %v, want error", out["status"])
	}
	if data, _ := out["data"].(map[string]any); data == nil {
		t.Fatalf("data missing: %v", out["data"])
	}
	if out["cause"] != "disabled" {
		t.Errorf("cause = %v, want disabled — a module-level refusal has its own token, so the panel says 联盟未启用 instead of a generic unknown", out["cause"])
	}
}
