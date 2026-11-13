<script setup>
// MyReportsPage: the player's own report tracking — what they reported, why,
// and how it turned out. Before this page reporting was a one-way door: the
// submit dialog on the detail page was the only surface, so a player could
// never learn whether anything came of it. The list is scoped to the caller
// server-side (ListMyReports); moderation internals stay out of the payload —
// the outcome is the status and the handled time, not the operator's notes.
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import { formatWhen } from '../format.js'
import EmptyState from '../components/EmptyState.vue'
import Pager from '../components/Pager.vue'

const { t } = useI18n()

const rows = ref([])
const total = ref(0)
const page = ref(1)
const perPage = 20
const error = ref('')

const pages = computed(() => Math.max(1, Math.ceil(total.value / perPage)))

// The reason set is the same closed six the detail-page dialog submits (and
// the admin list labels) — one dictionary, three surfaces.
const reasons = {
  copyright: 'admin.reasonCopyright',
  sexual: 'admin.reasonSexual',
  violence: 'admin.reasonViolence',
  hate: 'admin.reasonHate',
  spam: 'admin.reasonSpam',
  other: 'admin.reasonOther',
}
const statuses = {
  open: 'myreports.statusOpen',
  resolved: 'myreports.statusResolved',
  dismissed: 'myreports.statusDismissed',
}

async function load() {
  error.value = ''
  try {
    const d = await api.myReports(page.value, perPage)
    rows.value = d.reports || []
    total.value = d.total || 0
  } catch (e) { if (!guard401(e)) error.value = e.message }
}

function reasonText(r) {
  return t(reasons[r.reason] || 'admin.reasonOther')
}

function statusText(r) {
  return t(statuses[r.status] || 'myreports.statusOpen')
}

function textureLabel(r) {
  // The texture may be gone entirely; the join keeps the row, so the link
  // still has to read as something.
  return r.textureName || ('#' + r.textureId)
}

function when(tm) {
  return formatWhen(tm)
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('myreports.title') }}</h2>
    <div class="error" role="alert" v-if="error">{{ error }}</div>
    <p class="muted">{{ t('myreports.hint') }}</p>

    <table v-if="rows.length">
      <caption class="sr-only">{{ t('myreports.title') }}</caption>
      <tr>
        <th scope="col">{{ t('myreports.colTexture') }}</th>
        <th scope="col">{{ t('myreports.colReason') }}</th>
        <th scope="col">{{ t('myreports.colStatus') }}</th>
        <th scope="col">{{ t('myreports.colSubmitted') }}</th>
        <th scope="col">{{ t('myreports.colHandled') }}</th>
        <th scope="col"></th>
      </tr>
      <tr v-for="r in rows" :key="r.id">
        <td>
          <img v-if="r.previewURL" :src="r.previewURL" :alt="t('myreports.colTexture')"
               style="height: 32px; image-rendering: pixelated; vertical-align: middle; margin-right: 8px;"
               loading="lazy" decoding="async" />
          <a :href="`/skinlib/${r.textureId}`">{{ textureLabel(r) }}</a>
        </td>
        <td>{{ reasonText(r) }}</td>
        <td><span class="status" :class="r.status">{{ statusText(r) }}</span></td>
        <td class="muted">{{ when(r.createdAt) }}</td>
        <td class="muted">{{ r.handledAt ? when(r.handledAt) : '—' }}</td>
        <td><a class="secondary btn-link" :href="`/skinlib/${r.textureId}`">{{ t('myreports.view') }}</a></td>
      </tr>
    </table>
    <EmptyState v-else :text="t('myreports.empty')" />
    <Pager :page="page" :pages="pages" :total="total" @update:page="(p) => { page = p; load() }" />
  </div>
</template>

<style scoped>
.status { font-size: var(--fs-s); }
.status.open { color: var(--moon-2); }
.status.resolved { color: var(--ok); }
.status.dismissed { color: var(--muted); }
</style>
