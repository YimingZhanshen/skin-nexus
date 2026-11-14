<script setup>
// ProfilePage (A 级批次): 个人资料 —— 改昵称/改邮箱/改密码/头像（衣柜皮肤
// 选设 + 重置）/删除账号（密码复核 + 双重确认；管理员不可自助删）。
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import { confirmDialog } from '../dialog.js'
import PluginSlot from '../components/plugin/PluginSlot.vue'
import WearTexturePicker from '../components/WearTexturePicker.vue'

const { t } = useI18n()

// Same gating rule as the forgot-password link: a surface that cannot possibly
// work on this deployment is not shown.
defineProps({ mailEnabled: { type: Boolean, default: false } })

const me = ref(null)
const avatarPickOpen = ref(false)
const err = ref('')
const ok = ref('')

// profile edit
const newNickname = ref('')
const newEmail = ref('')

// password change
const curPw = ref('')
const newPw = ref('')
const pwBusy = ref(false)
const mailBusy = ref(false)

// account delete
const delPw = ref('')
const delBusy = ref(false)

const avatarUrl = computed(() => (me.value && me.value.avatarHash)
  ? `/textures/${me.value.avatarHash}` : '')

// 头像就是衣柜里的一张皮肤，所以候选集与「穿到角色」用的是同一个弹窗：以前这里
// 是一条把整个衣柜摊进 <option> 的下拉，几百项时既没图也没法搜。

async function load() {
  err.value = ''
  try {
    const u = await api.me()
    me.value = u
    newNickname.value = u.nickname || ''
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

async function saveProfile() {
  err.value = ''; ok.value = ''
  try {
    const d = await api.updateProfile(newNickname.value.trim(), newEmail.value.trim())
    if (d.ok) {
      ok.value = t('profile.saved')
      newEmail.value = ''
      try { me.value = await api.me() } catch (e) { if (!guard401(e)) err.value = e.message }
    }
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

async function changePassword() {
  if (pwBusy.value) return
  err.value = ''; ok.value = ''; pwBusy.value = true
  try {
    await api.changePassword(curPw.value, newPw.value)
    ok.value = t('auth.pwChanged')
    curPw.value = ''; newPw.value = ''
  } catch (e) { if (!guard401(e)) err.value = e.message }
  finally { pwBusy.value = false }
}

async function requestEmailVerification() {
  if (mailBusy.value) return
  err.value = ''; ok.value = ''; mailBusy.value = true
  try {
    await api.requestEmailVerification()
    ok.value = t('auth.verifySent')
  } catch (e) { if (!guard401(e)) err.value = e.message }
  finally { mailBusy.value = false }
}

async function setAvatarTid(tid) {
  avatarPickOpen.value = false
  if (!tid) return resetAvatar()
  err.value = ''; ok.value = ''
  try {
    await api.setAvatar(tid)
    ok.value = t('closet.avatarSet')
    try { me.value = await api.me() } catch (e) { if (!guard401(e)) err.value = e.message }
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

async function resetAvatar() {
  err.value = ''; ok.value = ''
  try {
    await api.resetAvatar()
    ok.value = t('profile.avatarReset')
    try { me.value = await api.me() } catch (e) { if (!guard401(e)) err.value = e.message }
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

// 删除账号：密码复核 + 双重确认（服务端还有 permission>0 硬门）。
// 成功后后端清 cookie；前端回登录页。
async function deleteAccount() {
  if (!(await confirmDialog(t('profile.deleteConfirm'), { danger: true }))) return
  if (!(await confirmDialog(t('profile.deleteDouble', { name: me.value?.nickname || '' }), { danger: true }))) return
  if (delBusy.value) return
  err.value = ''; delBusy.value = true
  try {
    await api.deleteAccount(delPw.value)
    location.reload() // session gone — full reset to the login view
  } catch (e) { if (!guard401(e)) err.value = e.message; delBusy.value = false }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('profile.title') }}</h2>
    <div class="error" role="alert" v-if="err">{{ err }}</div>
    <div class="success" role="status" v-if="ok">{{ ok }}</div>

    <!-- avatar -->
    <h3>{{ t('profile.avatarSection') }}</h3>
    <div class="row avatar-row">
      <!-- face crop: MC skin front-head sits at (8,8)-(16,16); scale the
           64px texture 8× and offset to that tile (same layout on 64×32
           legacy skins — top half is identical) -->
      <div class="avatar-box">
        <div v-if="avatarUrl" class="avatar-face" :style="{ backgroundImage: `url(${avatarUrl})` }"></div>
        <div v-else class="avatar-empty">{{ t('profile.avatarDefault') }}</div>
      </div>
      <div class="row" style="flex-wrap: wrap;">
        <button class="secondary" @click="avatarPickOpen = true">{{ t('profile.avatarPick') }}</button>
        <button class="secondary" v-if="avatarUrl" @click="resetAvatar">{{ t('profile.avatarResetBtn') }}</button>
      </div>
    </div>

    <!-- nickname / email -->
    <h3>{{ t('profile.infoSection') }}</h3>
    <div class="row">
      <label class="fld">{{ t('auth.nickname') }}
        <input v-model="newNickname" />
      </label>
      <label class="fld">{{ t('profile.newEmail') }}
        <input v-model="newEmail" type="email" :placeholder="t('profile.emailKeepHint')" />
      </label>
    </div>
    <div class="row" style="margin-top: 8px;">
      <button @click="saveProfile">{{ t('common.save') }}</button>
      <button class="secondary" v-if="mailEnabled && me && !me.emailVerified" :disabled="mailBusy" @click="requestEmailVerification">{{ mailBusy ? t('auth.verifySubmitting') : t('auth.verifySubmit') }}</button>
      <!-- 状态是徽章不是行内灰字：一眼可辨且不与提示文本混流 -->
      <span v-if="me" class="badge" :class="me.emailVerified ? 'ok' : 'danger'">{{ me.emailVerified ? t('profile.verified') : t('profile.unverified') }}</span>
    </div>
    <span class="field-hint">{{ t('profile.emailResetHint') }}</span>

    <!-- password -->
    <h3>{{ t('auth.pwTitle') }}</h3>
    <div class="row">
      <input v-model="curPw" type="password" :placeholder="t('auth.pwCurrent')" style="max-width: 220px;" />
      <input v-model="newPw" type="password" :placeholder="t('auth.pwNew')" style="max-width: 220px;" @keyup.enter="changePassword" />
      <button :disabled="pwBusy" @click="changePassword">{{ t('auth.pwSubmit') }}</button>
    </div>
    <span class="field-hint">{{ t('auth.pwHint') }}</span>

    <!-- delete account (admins blocked server-side)：危险区独立描边小卡 -->
    <div class="danger-zone">
      <h3 class="danger-text">{{ t('profile.dangerZone') }}</h3>
      <div class="row">
        <input v-model="delPw" type="password" :placeholder="t('profile.deletePwPlaceholder')" style="max-width: 260px;" />
        <button class="danger" :disabled="delBusy" @click="deleteAccount">{{ t('profile.deleteBtn') }}</button>
      </div>
      <span class="field-hint">{{ t('profile.deleteHint') }}</span>
    </div>
    <PluginSlot slot="profile.card.footer" />
  </div>

  <WearTexturePicker
    :open="avatarPickOpen"
    type="skin"
    :texture-hash="me && me.avatarHash ? me.avatarHash : ''"
    @close="avatarPickOpen = false"
    @pick="setAvatarTid"
  />
</template>

<style scoped>
h3 { font-size: var(--fs-s); margin: 20px 0 8px; color: var(--muted); font-weight: 600; }
.avatar-row { gap: 16px; }
.avatar-box { width: 64px; height: 64px; border: 1px solid var(--border); border-radius: 8px; display: flex; align-items: center; justify-content: center; overflow: hidden; }
/* face crop (legacy parity: TextureController render2dAvatar = front-head
   render): face tile at (8,8)-(16,16); 6× scale puts the 8px face exactly
   into the 48px box; `auto` height keeps 64×32 legacy skins undistorted
   (top half shares the layout). */
.avatar-face {
  width: 48px; height: 48px;
  background-repeat: no-repeat;
  background-size: 384px auto;
  background-position: -48px -48px;
  image-rendering: pixelated;
  border-radius: 2px;
}
.avatar-empty { color: var(--muted); font-size: var(--fs-xs); }
.fld { flex: 1; min-width: 220px; }
.fld input { margin-top: 4px; }
.danger-text { color: var(--danger); }
/* 危险区独立成块：细 danger 描边小卡，与上方账号区安静分层 */
.danger-zone {
  margin-top: 24px; padding: 16px;
  border: 1px solid color-mix(in srgb, var(--danger) 35%, transparent);
  border-radius: var(--r-m);
}
.danger-zone h3 { margin-top: 0; }
</style>
