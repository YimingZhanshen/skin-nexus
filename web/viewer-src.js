// viewer-src.js — fixed-name entry (/assets/viewer.js) for the
// server-rendered texture detail page (texturepage.go).
//
// Progressive enhancement contract: the page ships a complete static
// <img> preview that crawlers and no-JS visitors read; this module upgrades
// it into an auto-rotating skinview3d canvas and wires the like toggle.
// Nothing here is required to understand the page — SEO content lives in
// the server-rendered HTML, not in this file.
//
// skinview3d is dynamically imported only when a canvas is actually present
// (i.e. a skin, not a cape) and WebGL is available, so the ~500 KB chunk
// never loads on cape pages or legacy browsers.
//
// Wording comes from the page, not from this file: texturepage.go injects
// `window.__SKIN_I18N__ = {lang, strings}` from the same catalog that rendered
// the surrounding HTML, so a message cannot come out in the wrong language and
// this module carries no translation table of its own.
const STR = (window.__SKIN_I18N__ && window.__SKIN_I18N__.strings) || {}
const msg = (key) => STR[key] || ''

async function upgradeViewer() {
  const wrap = document.getElementById('tex-viewer')
  const canvas = wrap && wrap.querySelector('canvas[data-skinview3d]')
  if (!wrap || !canvas) return

  // WebGL probe: without it skinview3d throws — keep the static preview.
  try {
    if (!canvas.getContext('webgl2') && !canvas.getContext('webgl')) return
  } catch { return }

  try {
    const skinview3d = await import('skinview3d')
    const viewer = new skinview3d.SkinViewer({
      canvas,
      width: Math.min(320, wrap.clientWidth || 320),
      height: 420,
      zoom: 0.9,
      autoRotate: true,
    })
    const model = canvas.dataset.model === 'slim' ? 'slim' : 'default'
    await viewer.loadSkin(canvas.dataset.texture, { model })
    // Only hide the static preview once the first frame is actually up —
    // a failed load leaves the <img> in place instead of a blank box.
    wrap.classList.add('ready')
  } catch {
    /* keep static preview */
  }
}

function wireLikeButton() {
  const btn = document.querySelector('[data-like]')
  if (!btn) return
  const tid = btn.getAttribute('data-like')
  const countEl = document.getElementById('like-count')
  btn.addEventListener('click', async () => {
    const liked = btn.getAttribute('data-liked') === '1'
    btn.disabled = true
    // 失败可见（2026-10-27 逻辑修复：原静默 return）：按钮旁浮出 3 秒提示
    const tip = (msg) => {
      let el = document.getElementById('like-tip')
      if (!el) {
        el = document.createElement('span')
        el.id = 'like-tip'
        el.style.cssText = 'color:#d9564c;font-size:13px;margin-left:8px'
        btn.parentElement.appendChild(el)
      }
      el.textContent = msg
      setTimeout(() => el.remove(), 3000)
    }
    try {
      const res = await fetch(`/api/v1/skinlib/${tid}/like`, {
        method: liked ? 'DELETE' : 'POST',
        headers: { 'Content-Type': 'application/json' },
      })
      // 401：带 next 回跳，登录后回到本详情页（原直接 /login 丢失上下文）
      if (res.status === 401) {
        location.href = '/login?next=' + encodeURIComponent(location.pathname)
        return
      }
      if (!res.ok) { tip(msg('likeFailed')); return }
      const data = await res.json().catch(() => null)
      btn.setAttribute('data-liked', liked ? '0' : '1')
      btn.classList.toggle('liked', !liked)
      if (data && typeof data.likes === 'number' && countEl) {
        countEl.textContent = String(data.likes)
      }
    } catch { tip(msg('network')) }
    finally { btn.disabled = false }
  })
}

function wireReportButton() {
  const btn = document.getElementById('report-btn')
  const dlg = document.getElementById('report-dialog')
  const form = document.getElementById('report-form')
  if (!btn || !dlg || !form) return
  const err = document.getElementById('report-error')
  const cancel = document.getElementById('report-cancel')
  const show = (msg) => { err.textContent = msg; err.hidden = !msg }
  btn.addEventListener('click', () => { show(''); dlg.showModal() })
  cancel.addEventListener('click', () => dlg.close())
  // 0/500 计数与上传描述同语言：让「选填」的边界可见
  const detail = document.getElementById('report-detail')
  const count = document.getElementById('report-count')
  if (detail && count) {
    detail.addEventListener('input', () => { count.textContent = detail.value.length + ' / 500' })
  }
  form.addEventListener('submit', async (e) => {
    e.preventDefault()
    const tid = btn.getAttribute('data-tid')
    const submit = form.querySelector('button[type="submit"]')
    submit.disabled = true
    show('')
    try {
      const reason = document.getElementById('report-reason').value
      const detail = document.getElementById('report-detail').value.trim()
      const res = await fetch(`/api/v1/skinlib/${encodeURIComponent(tid)}/report`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason, detail }),
      })
      if (res.status === 401) {
        location.href = '/login?next=' + encodeURIComponent(location.pathname)
        return
      }
      const data = await res.json().catch(() => ({}))
      if (res.ok) {
        form.replaceChildren(Object.assign(document.createElement('p'), {
          className: 'muted',
          textContent: msg('reportDone'),
        }))
        return
      }
      // data.message stays first: the API answers with its own prose for codes
      // this page does not carry a table entry for.
      show(data.message || (res.status === 409 ? msg('reportDuplicate') : msg('reportFailed')))
    } catch {
      show(msg('network'))
    } finally {
      submit.disabled = false
    }
  })
}

function wireEditButton() {
  const btn = document.getElementById('edit-btn')
  const dlg = document.getElementById('edit-dialog')
  const form = document.getElementById('edit-form')
  if (!btn || !dlg || !form) return
  const err = document.getElementById('edit-error')
  const show = (msg) => { err.textContent = msg; err.hidden = !msg }
  btn.addEventListener('click', () => { show(''); dlg.showModal() })
  document.getElementById('edit-cancel').addEventListener('click', () => dlg.close())
  form.addEventListener('submit', async (e) => {
    e.preventDefault()
    const tid = btn.closest('article')?.querySelector('[data-like]')?.getAttribute('data-like') ||
      location.pathname.split('/').pop()
    const src = document.getElementById('edit-source').value
    const sourceUrl = document.getElementById('edit-source-url').value.trim()
    const body = {
      name: document.getElementById('edit-name').value.trim(),
      detail: document.getElementById('edit-detail').value,
      source: src,
      sourceUrl,
    }
    if ((src === 'derivative' || src === 'repost') && !/^https?:\/\//i.test(sourceUrl)) {
      show(msg('sourceUrlRequired'))
      return
    }
    const submit = form.querySelector('button[type="submit"]')
    submit.disabled = true
    try {
      const res = await fetch(`/api/v1/skinlib/${encodeURIComponent(tid)}`, {
        method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
      })
      if (res.status === 401) { location.href = '/login?next=' + encodeURIComponent(location.pathname); return }
      const data = await res.json().catch(() => ({}))
      if (!res.ok) { show(data.message || msg('saveFailed')); return }
      location.reload()
    } catch { show(msg('network')) }
    finally { submit.disabled = false }
  })
}

upgradeViewer()
wireLikeButton()
wireReportButton()
wireEditButton()
