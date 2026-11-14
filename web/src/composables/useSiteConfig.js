// useSiteConfig.js — the deployment's own capabilities, fetched once at boot.
//
// The auth forms follow the real configuration instead of hardcoding it:
// the invite field is required-or-absent, the email field and the forgot flow
// are available-or-hidden, the union tab exists only where the module is on.
// Until the fetch resolves (or if it fails) the pre-config UI stands, which is
// today's behavior.
//
// The ref lives here and is provided to the tree by App.vue under the frozen
// key `siteConfig` — pages inject that key, never this module.
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'

export function useSiteConfig() {
  const { t } = useI18n()

  function defaultFooter() {
    return { text: '', links: [], siteName: 'SkinNexus', year: new Date().getFullYear() }
  }

  function normalizeFooter(raw) {
    const f = raw && typeof raw === 'object' ? raw : {}
    return { ...defaultFooter(), ...f, links: Array.isArray(f.links) ? f.links : [] }
  }

  const siteConf = ref({
    site_name: '', invite_required: false, mail_configured: false,
    verify_gate_enabled: false, union_enabled: false, oidc_enabled: false, max_upload_kb: 0,
    footer: defaultFooter(),
  })
  const confLoaded = ref(false)

  const siteFooter = computed(() => normalizeFooter(siteConf.value.footer))
  const siteName = computed(() => siteConf.value.site_name || siteFooter.value.siteName || t('site.name'))

  // Invite field is absent unless the server says it is required. The old
  // "show an optional box while config loads" behavior produced a dead
  // "邀请码（选填）" field on sites that never use invitations.
  const inviteFieldShown = computed(() => confLoaded.value && siteConf.value.invite_required)
  const inviteIsRequired = computed(() => confLoaded.value && siteConf.value.invite_required)
  const mailEnabled = computed(() => confLoaded.value && siteConf.value.mail_configured)
  // The join gate is enforced at the server door and needs an address to mail, so
  // with the gate on the email field must appear even when SMTP is not configured
  // yet, and it must be filled: an account without an address can never pass.
  const verifyGateEnabled = computed(() => confLoaded.value && siteConf.value.verify_gate_enabled === true)
  const emailFieldShown = computed(() => mailEnabled.value || verifyGateEnabled.value)
  const unionEnabled = computed(() => confLoaded.value && siteConf.value.union_enabled)
  // The authorized-apps and device pages talk to /api/v1/oidc/*, which only
  // exists when the OIDC provider is mounted. Offer the entries only then.
  const oidcEnabled = computed(() => confLoaded.value && siteConf.value.oidc_enabled)

  return {
    siteConf, confLoaded, defaultFooter, normalizeFooter,
    siteFooter, siteName, inviteFieldShown, inviteIsRequired,
    mailEnabled, verifyGateEnabled, emailFieldShown, unionEnabled, oidcEnabled,
  }
}
