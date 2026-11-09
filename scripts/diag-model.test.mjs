// scripts/diag-model.test.mjs — node --test, no new dependency.
//
// web/ has no component test framework, but the diagnose report's meaning is
// decided by a pure function, so it does not need one. These cases are the
// regression the round exists for: the hub's payload and our own failure
// evidence reuse four key names, and reading them flat put the hub's probe
// results next to a "both directions fine" banner in production.
//
// The oracle is the key name, not prose: a row that lands in the wrong leg
// fails even if it renders beautifully.

import test from 'node:test'
import assert from 'node:assert/strict'

import { buildDiagModel, diagSummaryOf, diagMetaOf, diagJSONOf, certState, summarizeTrace } from '../web/src/diagModel.js'

// t() stands in for vue-i18n: it returns the key (plus params), so an
// assertion can name exactly which label a row chose.
const t = (key, params) => (params ? `${key}(${JSON.stringify(params)})` : key)

const hubPayload = {
  healthy: true,
  delay: 0.00305,
  timestamp: 1794238597.123,
  tls_handshake: [
    '|<5>| REC[0x64f6b6f6b380]: Allocating epoch #0',
    '|<4>| HSK[0x64f6b6f6b380]: Adv. version: 3.3',
    '|<2>| Keeping ciphersuite 13.02 (GNUTLS_AES_256_GCM_SHA384)',
    '|<3>| ASSERT: ../../.././lib/x509/dn.c[_gnutls_x509_compare_raw_dn]:974',
    '|<2>| Keeping ciphersuite 13.03 (GNUTLS_CHACHA20_POLY1305_SHA256)',
  ].join('\n'),
  status_code: 200,
  headers: { Server: ['nginx'], Date: ['Mon, 09 Nov 2026 15:36:37 GMT'] },
  body: '{"nonce":"abc","timestamp":1794238597.1}',
}

const probe = {
  tls: { version: 'TLS 1.3', cipher: 'TLS_AES_256_GCM_SHA384', cert_not_after: '2027-02-01T00:00:00Z', cert_days_left: 84 },
  clock: { hub_clock: 'Mon, 09 Nov 2026 15:36:37 GMT', offset_seconds: 1.4, within_window: true },
}

function legsOf(report) {
  const legs = buildDiagModel(report, t)
  return { out: legs.find((l) => l.key === 'out'), inn: legs.find((l) => l.key === 'in') }
}

test('a successful leg one puts the hub payload in leg two, never in leg one', () => {
  const { out, inn } = legsOf({ status: 'ok', data: hubPayload, probe })
  const outKeys = out.rows.map((r) => r.k)
  for (const leaked of ['admin.diagStatusCode', 'admin.diagHeaders', 'admin.diagBody', 'admin.diagTLS']) {
    assert.ok(!outKeys.includes(leaked), `${leaked} describes the hub's probe of us and must not appear under "this site -> hub"`)
  }
  const inKeys = inn.rows.map((r) => r.k)
  for (const expected of ['admin.diagStatusCode', 'admin.diagHeaders', 'admin.diagBody', 'admin.diagTLS', 'admin.diagTLSRaw']) {
    assert.ok(inKeys.includes(expected), `${expected} belongs to leg two and is missing`)
  }
  assert.equal(inn.state, 'ok')
})

test('a failed leg one files status_code, headers and body as OUR evidence', () => {
  const { out, inn } = legsOf({
    status: 'error',
    cause: 'http_key',
    data: { status_code: 403, headers: { Server: ['nginx'] }, body: 'denied' },
  })
  assert.equal(out.state, 'fail')
  const cause = out.rows.find((r) => r.k === 'admin.diagCause')
  assert.equal(cause.v, 'admin.diagCauseHttpKey', 'a rejected member key must not read as a network problem')
  assert.equal(cause.mono, 'http_key', 'the token itself stays quotable in a bug report')
  assert.deepEqual(out.rows.filter((r) => r.k === 'admin.diagStatusCode').map((r) => r.v), ['403'])
  // The hub never got to probe us: inventing a verdict would be a lie.
  assert.equal(inn.state, 'skip')
  assert.equal(inn.rows[0].v, 'admin.diagNotRun')
})

test('an unreadable clock is a warning, not a green tick', () => {
  const { out } = legsOf({ status: 'ok', data: { healthy: true }, probe: { tls: probe.tls } })
  const clock = out.rows.find((r) => r.k === 'admin.diagClock')
  assert.equal(clock.v, 'admin.diagClockUnknown')
  assert.equal(clock.state, 'warn')
  assert.equal(diagSummaryOf(buildDiagModel({ status: 'ok', data: { healthy: true }, probe: { tls: probe.tls } }, t)), 'warn')
})

test('a clock outside the signature window warns without calling it a failure', () => {
  const report = {
    status: 'ok',
    data: { healthy: true },
    probe: { tls: probe.tls, clock: { hub_clock: 'x', offset_seconds: 120, within_window: false } },
  }
  const { out } = legsOf(report)
  const clock = out.rows.find((r) => r.k === 'admin.diagClock')
  assert.equal(clock.state, 'warn')
  assert.equal(clock.v, '120.0 s · admin.diagClockOutside', 'the number and the verdict travel together')
  assert.equal(diagSummaryOf(buildDiagModel(report, t)), 'warn')
})

test('plain HTTP to the hub is stated, not left blank', () => {
  const { out } = legsOf({ status: 'ok', data: { healthy: true }, probe: { clock: probe.clock } })
  const tlsRow = out.rows.find((r) => r.k === 'admin.diagTLSNeg')
  assert.equal(tlsRow.v, 'admin.diagPlainHTTP')
  assert.equal(tlsRow.state, 'warn')
})

test('certificate thresholds', () => {
  assert.equal(certState(400), 'ok')
  assert.equal(certState(14), 'ok')
  assert.equal(certState(13), 'warn')
  assert.equal(certState(0), 'warn')
  assert.equal(certState(-1), 'fail')
  const { out } = legsOf({
    status: 'ok',
    data: { healthy: true },
    probe: { tls: { version: 'TLS 1.3', cipher: 'X', cert_not_after: '2026-11-01T00:00:00Z', cert_days_left: -8 }, clock: probe.clock },
  })
  const cert = out.rows.find((r) => r.k === 'admin.diagCert')
  assert.equal(cert.state, 'fail')
  assert.match(cert.v, /admin\.diagCertExpired\(\{"days":8\}\)/, 'expired reads as expired, not as negative days')
})

test('the GnuTLS trace is summarised, and the raw text stays available', () => {
  const { inn } = legsOf({ status: 'ok', data: hubPayload, probe })
  const summary = inn.rows.find((r) => r.k === 'admin.diagTLS')
  assert.match(summary.v, /3\.3/, 'the advertised protocol version is the readable half')
  assert.match(summary.v, /13\.03 \(GNUTLS_CHACHA20_POLY1305_SHA256\)/, 'the last kept suite, not the first')
  assert.match(summary.v, /admin\.diagTraceAsserts\(\{"count":1\}\)/)
  const raw = inn.rows.find((r) => r.k === 'admin.diagTLSRaw')
  assert.match(raw.pre, /ASSERT: /, 'the operator can still expand the full trace')
})

test('summarizeTrace degrades on text with nothing recognisable', () => {
  const out = summarizeTrace('complete garbage\nsecond line', t)
  assert.equal(out, 'complete garbage', 'fall back to the first line rather than an empty cell')
})

test('an unknown cause token still gets a sentence', () => {
  const { out } = legsOf({ status: 'error', cause: 'something_new_in_the_hub', data: { exception: 'boom' } })
  assert.equal(out.rows.find((r) => r.k === 'admin.diagCause').v, 'admin.diagCauseUnknown')
})

test('a zero-valued hub field is not silently dropped', () => {
  // The legacy rows used truthiness, so delay 0 and status_code 0 vanished.
  const { inn } = legsOf({ status: 'ok', data: { healthy: true, delay: 0 }, probe })
  assert.ok(inn.rows.some((r) => r.k === 'admin.diagDelay'), 'a 0 ms delay is a measurement, not an absence')
})

test('the JSON export carries both the interpretation and the raw payload', () => {
  const report = { status: 'ok', data: hubPayload, probe, key_fingerprint: 'SHA256:6000acb2' }
  const out = diagJSONOf(report, buildDiagModel(report, t), '2026-11-10T00:00:00.000Z')
  assert.equal(out.generated_at, '2026-11-10T00:00:00.000Z')
  assert.deepEqual(out.report, report, 'the untouched hub payload is what makes the report arguable')
  assert.equal(out.legs.length, 2)
  assert.equal(out.legs[1].rows.find((r) => r.k === 'admin.diagStatusCode').v, '200')
})

test('the meta line is labelled and empty-safe', () => {
  const meta = diagMetaOf({ key_fingerprint: 'SHA256:6000acb2', private_key_version: 1, server_list_version: 781 }, t)
  assert.deepEqual(meta.map((m) => m.k), [
    'admin.diagKeyFingerprint', 'admin.diagPrivateKeyVersion', 'admin.diagServerListVersion',
  ])
  assert.equal(meta[2].v, 781)
  assert.deepEqual(diagMetaOf({ status: 'ok' }, t), [], 'no fingerprint, no half-filled line')
})
