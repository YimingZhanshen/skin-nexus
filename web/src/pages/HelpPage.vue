<script setup>
// HelpPage: the answers a player actually needs, written only where this
// project can answer correctly. Anything that depends on a third-party
// server's config is phrased as "this lives with your server admin", not as a
// promise we cannot keep.
//
// Reachable while logged out on purpose: the most common reason someone opens
// this page is that they cannot get in.
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatBytes } from '../format.js'

const { t } = useI18n()
const goto = inject('gotoView', null)
const siteConf = inject('siteConfig', null)
const conf = computed(() => (siteConf && siteConf.value) || {})
const uploadLimit = computed(() => formatBytes(Number(conf.value.max_upload_kb || 1024) * 1024))
const verifyGate = computed(() => conf.value.verify_gate_enabled === true)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('help.title') }}</h2>
    <p class="muted">{{ t('help.lead') }}</p>

    <h3>{{ t('help.notShownTitle') }}</h3>
    <p class="muted">{{ t('help.notShownLead') }}</p>
    <ol class="help-list">
      <li>
        <b>{{ t('help.notShownLauncher') }}</b>
        <span>{{ t('help.notShownLauncherBody') }}</span>
      </li>
      <li>
        <b>{{ t('help.notShownName') }}</b>
        <span>{{ t('help.notShownNameBody') }}</span>
        <button v-if="goto" class="link-like" @click="goto('players')">{{ t('help.goPlayers') }}</button>
      </li>
      <li>
        <b>{{ t('help.notShownServer') }}</b>
        <span>{{ t('help.notShownServerBody') }}</span>
      </li>
      <li>
        <b>{{ t('help.notShownDomain') }}</b>
        <span>{{ t('help.notShownDomainBody') }}</span>
      </li>
      <li>
        <b>{{ t('help.notShownPrivate') }}</b>
        <span>{{ t('help.notShownPrivateBody') }}</span>
        <button v-if="goto" class="link-like" @click="goto('closet')">{{ t('help.goCloset') }}</button>
      </li>
      <li v-if="verifyGate">
        <b>{{ t('help.notShownVerify') }}</b>
        <span>{{ t('help.notShownVerifyBody') }}</span>
        <button v-if="goto" class="link-like" @click="goto('profile')">{{ t('help.goProfile') }}</button>
      </li>
    </ol>

    <h3>{{ t('help.launcherTitle') }}</h3>
    <p class="muted">{{ t('help.launcherBody') }}</p>

    <h3>{{ t('help.uploadTitle') }}</h3>
    <ul class="help-flat">
      <li>{{ t('help.uploadSizes') }}</li>
      <li>{{ t('help.uploadSize', { size: uploadLimit }) }}</li>
      <li>{{ t('help.uploadName') }}</li>
    </ul>

    <h3>{{ t('help.closetTitle') }}</h3>
    <p class="muted">{{ t('help.closetBody') }}</p>

    <h3>{{ t('help.nameTitle') }}</h3>
    <p class="muted">{{ t('help.nameBody') }}</p>

    <h3>{{ t('help.passwordTitle') }}</h3>
    <p class="muted">{{ t('help.passwordBody') }}</p>
  </div>
</template>

<style scoped>
h3 { margin: 22px 0 4px; font-size: var(--fs-m); }
.help-list { margin: 8px 0 0; padding-left: 22px; display: grid; gap: 10px; }
.help-list li { display: grid; gap: 2px; }
.help-list b { font-weight: 600; }
.help-list span { color: var(--muted); font-size: var(--fs-s); }
.help-flat { margin: 8px 0 0; padding-left: 22px; color: var(--muted); font-size: var(--fs-s); }
.link-like {
  background: none; border: none; padding: 0; width: fit-content; min-height: 0;
  color: var(--jade); cursor: pointer; font-size: var(--fs-s);
}
.link-like:hover { color: var(--jade-hi); text-decoration: underline; }
/* The scoped min-height: 0 above outspecifies the global coarse-pointer rule, so
   on a phone these「链接一样的按钮」were 20px tall targets. */
@media (pointer: coarse) { .link-like { min-height: 44px; } }
</style>
