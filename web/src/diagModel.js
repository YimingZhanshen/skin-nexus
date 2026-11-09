// The diagnose report model, kept out of UnionView.vue so its semantics can
// be asserted (web/ has no component test framework; node --test does not
// need one for a pure function).
//
// WHY THE SHAPE IS WHAT IT IS
//
// The hub's diagnose payload and our own failure evidence use the same four
// key names — status_code, headers, body, tls_handshake — and they mean
// different things depending on whether leg one completed:
//
//   status ok     -> `data` is the hub describing ITS probe of this site
//   status error  -> `data` is the hub's answer to OUR request
//
// Reading them flat, which is what the legacy yggdrasil-connect modal did
// (config.twig:135-150) and what our first version copied, produces the
// nonsense an operator sees in production: a banner saying both directions
// are fine, sitting above a 状态码 200 and a response-header dump that belong
// to neither row. So every key is read in exactly one branch, and the two
// legs each carry their own verdict.
//
// `t` is passed in rather than imported: the component owns the i18n
// instance, and a model that takes its translator is testable without one.

export const CAUSE_TOKENS = [
  'http_key', 'http_path', 'http_busy', 'http_5xx', 'http_other',
  'dns', 'refused', 'tls', 'timeout', 'tcp', 'disabled', 'unknown',
]

// Cert expiry is the one number in the report with a date on which the site
// stops working, so it gets a threshold instead of a shrug. 14 days is the
// usual renew-before window for a 90-day certificate.
export function certState(days) {
  if (days < 0) return 'fail'
  if (days < 14) return 'warn'
  return 'ok'
}

// The hub hands back its GnuTLS debug trace: hundreds of lines of
// `|<3>| ASSERT: …/lib/x509/dn.c` that no one can read for a conclusion.
// What is in it: the version the hub's client advertised, the suite it kept,
// and how noisy the handshake was. The raw text stays available separately.
export function summarizeTrace(raw, t) {
  const lines = String(raw).split('\n')
  const adv = lines.find((l) => /Adv\. version:/.test(l))
  const suite = [...lines].reverse().find((l) => /Keeping ciphersuite/.test(l))
  const asserts = lines.filter((l) => /ASSERT/.test(l)).length
  const bits = []
  if (adv) bits.push(adv.split(':').slice(1).join(':').trim())
  if (suite) bits.push(suite.replace(/^.*Keeping /, '').trim())
  if (asserts) bits.push(t('admin.diagTraceAsserts', { count: asserts }))
  return bits.join(' · ') || lines[0].slice(0, 120)
}

function rawRow(k, value) {
  return { k, pre: String(value) }
}

export function buildDiagModel(d, t) {
  const data = d.data || {}
  const probe = d.probe || {}
  const legs = []

  // ---- leg one: this site -> hub -------------------------------------
  const out = { key: 'out', title: t('admin.diagSiteToUnion'), state: d.status === 'ok' ? 'ok' : 'fail', rows: [] }
  if (d.status === 'ok') {
    const tls = probe.tls
    if (tls) {
      out.rows.push({ k: t('admin.diagTLSNeg'), v: `${tls.version} · ${tls.cipher}`, state: 'ok' })
      if (tls.cert_not_after) {
        const days = tls.cert_days_left || 0
        out.rows.push({
          k: t('admin.diagCert'),
          v: days < 0
            ? `${tls.cert_not_after} · ${t('admin.diagCertExpired', { days: -days })}`
            : `${tls.cert_not_after} · ${t('admin.diagCertDays', { days })}`,
          state: certState(days),
        })
      }
    } else {
      // No TLS state means plain HTTP. Saying "unencrypted" beats showing an
      // empty row: it is a real finding about the hub URL.
      out.rows.push({ k: t('admin.diagTLSNeg'), v: t('admin.diagPlainHTTP'), state: 'warn' })
    }
    const clock = probe.clock
    if (clock) {
      out.rows.push({
        k: t('admin.diagClock'),
        v: `${clock.offset_seconds.toFixed(1)} s · ${clock.within_window ? t('admin.diagClockWithin') : t('admin.diagClockOutside')}`,
        state: clock.within_window ? 'ok' : 'warn',
      })
    } else {
      out.rows.push({ k: t('admin.diagClock'), v: t('admin.diagClockUnknown'), state: 'warn' })
    }
  } else {
    out.rows.push({
      k: t('admin.diagCause'),
      v: causeLabel(d.cause, t),
      mono: d.cause || 'unknown',
      state: 'fail',
    })
    if (data.status_code) out.rows.push({ k: t('admin.diagStatusCode'), v: String(data.status_code) })
    if (data.exception) out.rows.push(rawRow(t('admin.diagException'), data.exception))
    if (data.headers) out.rows.push(rawRow(t('admin.diagHeaders'), JSON.stringify(data.headers, null, 2)))
    if (data.body) out.rows.push(rawRow(t('admin.diagBody'), data.body))
  }
  legs.push(out)

  // ---- leg two: hub -> this site -------------------------------------
  const inn = { key: 'in', title: t('admin.diagUnionToSite'), rows: [] }
  if (d.status !== 'ok') {
    // The hub never got to probe us. Anything about that probe would be
    // invented, so say it was not measured instead of leaving a blank row.
    inn.state = 'skip'
    inn.rows.push({ k: t('admin.diagVerdict'), v: t('admin.diagNotRun'), state: 'skip' })
  } else {
    inn.state = data.healthy ? 'ok' : 'fail'
    inn.rows.push({ k: t('admin.diagVerdict'), state: data.healthy ? 'ok' : 'fail' })
    if (data.healthy && typeof data.delay === 'number') {
      inn.rows.push({ k: t('admin.diagDelay'), v: `${(data.delay * 1000).toFixed(2)} ms` })
    }
    if (data.timestamp) {
      // Our own clock, echoed back through the hub's probe of our inbound
      // endpoint (union/inbound.go handleDiagnose). Not the hub's time.
      inn.rows.push({ k: t('admin.diagTimestamp'), mono: String(data.timestamp) })
    }
    if (data.status_code) inn.rows.push({ k: t('admin.diagStatusCode'), v: String(data.status_code) })
    if (data.tls_handshake) {
      inn.rows.push({ k: t('admin.diagTLS'), v: summarizeTrace(data.tls_handshake, t) })
      inn.rows.push(rawRow(t('admin.diagTLSRaw'), data.tls_handshake))
    }
    if (data.headers) inn.rows.push(rawRow(t('admin.diagHeaders'), JSON.stringify(data.headers, null, 2)))
    if (data.body) inn.rows.push(rawRow(t('admin.diagBody'), data.body))
  }
  legs.push(inn)
  return legs
}

// Each cause token maps to one sentence an operator can act on. Written as a
// table of t('literal') calls, not a token -> key lookup, on purpose:
// scripts/check-locales.mjs only counts a key as used when the literal sits
// inside t(), so the lookup form would report every one of these as dead copy
// while the panel was happily rendering them.
export function causeLabel(token, t) {
  switch (token) {
    case 'http_key': return t('admin.diagCauseHttpKey')
    case 'http_path': return t('admin.diagCauseHttpPath')
    case 'http_busy': return t('admin.diagCauseHttpBusy')
    case 'http_5xx': return t('admin.diagCauseHttp5xx')
    case 'http_other': return t('admin.diagCauseHttpOther')
    case 'dns': return t('admin.diagCauseDns')
    case 'refused': return t('admin.diagCauseRefused')
    case 'tls': return t('admin.diagCauseTls')
    case 'timeout': return t('admin.diagCauseTimeout')
    case 'tcp': return t('admin.diagCauseTcp')
    case 'disabled': return t('admin.diagCauseDisabled')
    default: return t('admin.diagCauseUnknown')
  }
}

// A warning is not a soft failure: an expiring certificate or a clock outside
// the signature window is actionable and neither means the hub is unreachable.
// The legacy modal had only success/danger, so every warning was a red pill.
export function diagSummaryOf(legs) {
  const states = legs.map((l) => l.state)
  if (states.includes('fail')) return 'fail'
  if (states.includes('warn')) return 'warn'
  return legs.some((l) => l.rows.some((r) => r.state === 'warn')) ? 'warn' : 'ok'
}

export function diagMetaOf(d, t) {
  if (!d || !d.key_fingerprint) return []
  return [
    { k: t('admin.diagKeyFingerprint'), v: d.key_fingerprint },
    { k: t('admin.diagPrivateKeyVersion'), v: d.private_key_version || '—' },
    { k: t('admin.diagServerListVersion'), v: d.server_list_version || '—' },
  ]
}
