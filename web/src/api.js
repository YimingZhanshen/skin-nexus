// Thin fetch wrapper: same-origin cookies, JSON in/out.
import { fetchWithTimeout } from './net.js'

async function req(method, path, body, timeoutMS = 15000) {
  const opts = { method, headers: {} }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetchWithTimeout(path, opts, timeoutMS)
  if (res.status === 204) return null
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    // OIDC errors are {error}-only and some 401s are empty-bodied (review UI
    // P1): surface every message variant and carry the status for callers.
    const err = new Error(data.message || data.error || `HTTP ${res.status}`)
    err.status = res.status
    err.data = data
    throw err
  }
  return data
}

export const api = {
  me: () => req('GET', '/api/v1/me'),
  // Public capability flags: invite field required-or-absent on register,
  // email field + forgot flow available-or-hidden (A1 UX 收敛).
  siteConfig: () => req('GET', '/api/v1/site-config'),
  login: (identifier, password) => req('POST', '/api/v1/login', { identifier, password }),
  register: (email, nickname, password, invitationCode, pluginData) => req('POST', '/api/v1/register', { email, nickname, password, invitationCode, pluginData }),
  logout: () => req('POST', '/api/v1/logout'),
  changePassword: (currentPassword, newPassword) => req('POST', '/api/v1/me/password', { currentPassword, newPassword }),
  // A1 找回密码: forgot 恒 200（防邮箱枚举，未配置邮件时 503）；reset 消费
  // 单次令牌，失败统一「链接无效或已过期」。
  forgotPassword: (email) => req('POST', '/api/v1/password/forgot', { email }),
  resetPassword: (token, password) => req('POST', '/api/v1/password/reset', { token, password }),
  closet: () => req('GET', '/api/v1/me/closet'),
  closetAdd: (textureId, itemName = '') => req('POST', '/api/v1/me/closet', { textureId, itemName }),
  closetRename: (id, itemName) => req('PUT', `/api/v1/me/closet/${id}`, { itemName }),
  closetRemove: (id) => req('DELETE', `/api/v1/me/closet/${id}`),
  players: () => req('GET', '/api/v1/me/players'),
  createPlayer: (name) => req('POST', '/api/v1/me/players', { name }),
  textures: () => req('GET', '/api/v1/me/textures'),
  setSkin: (pid, textureId) => req('PUT', `/api/v1/me/players/${pid}/skin`, { textureId }),
  setCape: (pid, textureId) => req('PUT', `/api/v1/me/players/${pid}/cape`, { textureId }),
  // User-center self-service (A 级批次): profile edit / account delete /
  // avatar / own-player rename+delete.
  updateProfile: (nickname, email) => req('PUT', '/api/v1/me/profile', { nickname, email }),
  deleteAccount: (password) => req('DELETE', '/api/v1/me', { password }),
  setAvatar: (textureId) => req('PUT', '/api/v1/me/avatar', { textureId }),
  resetAvatar: () => req('DELETE', '/api/v1/me/avatar'),
  renamePlayer: (pid, name) => req('PUT', `/api/v1/me/players/${pid}/name`, { name }),
  deletePlayer: (pid) => req('DELETE', `/api/v1/me/players/${pid}`),
  // Skinlib (public library). Like = closet add (blessing semantics).
  skinlib: (params = {}) => {
    const qs = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) {
      if (v !== '' && v !== undefined && v !== null) qs.set(k, v)
    }
    const s = qs.toString()
    return req('GET', `/api/v1/skinlib${s ? `?${s}` : ''}`)
  },
  skinlibLike: (tid) => req('POST', `/api/v1/skinlib/${tid}/like`),
  skinlibUnlike: (tid) => req('DELETE', `/api/v1/skinlib/${tid}/like`),
  skinlibPatch: (tid, body) => req('PATCH', `/api/v1/skinlib/${tid}`, body),
}

// OIDC device flow + authorized-apps management.
export const oidc = {
  deviceInfo: (userCode) => req('GET', `/api/v1/oidc/device/info?user_code=${encodeURIComponent(userCode)}`),
  deviceSubmit: (userCode, decision, pid) => req('POST', '/api/v1/oidc/device/submit', { user_code: userCode, decision, pid }),
  tokens: () => req('GET', '/api/v1/oidc/tokens'),
  revokeToken: (body) => req('POST', '/api/v1/oidc/tokens/revoke', body),
}

// MUA Union member surface (routes exist only when the module is enabled).
export const union = {
  servers: () => req('GET', '/api/v1/union/servers'),
  detail: (uuid) => req('GET', `/api/v1/union/detail?uuid=${encodeURIComponent(uuid)}`),
  unmapped: (name) => req('GET', `/api/v1/union/unmapped?name=${encodeURIComponent(name)}`),
  bind: (uuid) => req('POST', '/api/v1/union/bind', { uuid }),
  bindto: (uuid, token) => req('POST', '/api/v1/union/bindto', { uuid, token }),
  unbind: (uuid) => req('POST', '/api/v1/union/unbind', { uuid }),
  remapuuid: (me, target) => req('POST', '/api/v1/union/remapuuid', { me, target }),
}

export function uploadTexture(type, file, model, description, pluginData, meta = {}) {
  return new Promise((resolve, reject) => {
    const fd = new FormData()
    fd.append('type', type)
    fd.append('file', file)
    if (model) fd.append('model', model)
    if (description) fd.append('description', description)
    if (meta.detail) fd.append('detail', meta.detail)
    fd.append('source', meta.source || '')
    if (meta.sourceUrl) fd.append('sourceUrl', meta.sourceUrl)
    fd.append('public', meta.public === false ? 'false' : 'true')
    if (pluginData && Object.keys(pluginData).length) fd.append('pluginData', JSON.stringify(pluginData))

    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/v1/me/textures')
    xhr.withCredentials = true
    xhr.timeout = 60000
    if (xhr.upload && meta.onProgress) {
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) meta.onProgress(Math.round((e.loaded / e.total) * 100))
      }
    }
    xhr.onload = () => {
      let data = {}
      try { data = JSON.parse(xhr.responseText || '{}') } catch { /* non-JSON error page */ }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(data)
        return
      }
      const err = new Error(data.message || data.error || `HTTP ${xhr.status}`)
      err.status = xhr.status
      err.data = data
      reject(err)
    }
    xhr.onerror = () => reject(new Error('网络异常，请稍后重试'))
    xhr.ontimeout = () => reject(new Error('上传超时，请重试'))
    xhr.send(fd)
  })
}
