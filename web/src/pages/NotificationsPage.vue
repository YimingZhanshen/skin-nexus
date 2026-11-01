<script setup>
// NotificationsPage (2026-11-02): the player's inbox. Rows are written by the
// admin surface when something happens to THEIR content or account; the kind is
// a closed set rendered from the locale dictionary (same shape as error codes),
// so a new kind needs a dictionary entry in both languages or the gate fails.
// Pull-only by design: the badge rides along on /me, there is no poll and no
// push channel.
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import EmptyState from '../components/EmptyState.vue'
import Pager from '../components/Pager.vue'

const { t } = useI18n()
const emit = defineEmits(['read'])

const rows = ref([])
const total = ref(0)
const page = ref(1)
const perPage = 20
const onlyUnread = ref(false)
const error = ref('')
const busy = ref(false)

const pages = computed(() => Math.max(1, Math.ceil(total.value / perPage)))

async function load() {
  error.value = ''
  try {
    const d = await api.notifications(page.value, perPage, onlyUnread.value)
    rows.value = d.notifications || []
    total.value = d.total || 0
  } catch (e) { if (!guard401(e)) error.value = e.message }
}

async function markRead(id) {
  busy.value = true
  try {
    await api.markNotificationsRead({ id })
    emit('read')
    await load()
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { busy.value = false }
}

async function markAllRead() {
  busy.value = true
  try {
    await api.markNotificationsRead({ all: true })
    emit('read')
    await load()
  } catch (e) { if (!guard401(e)) error.value = e.message } finally { busy.value = false }
}

function text(n) {
  return t('notifications.kinds.' + n.kind, n.ref ? { name: n.ref } : {})
}

function when(tm) {
  return new Date(tm).toLocaleString()
}

onMounted(load)
</script>

<template>
  <div class="card">
    <div class="row" style="justify-content: space-between;">
      <h2 style="margin: 0;">{{ t('notifications.title') }}</h2>
      <div class="row">
        <label class="filter">
          <input type="checkbox" v-model="onlyUnread" @change="page = 1; load()" />
          {{ t('notifications.unreadOnly') }}
        </label>
        <button class="secondary" :disabled="busy || !rows.some(r => !r.read)" @click="markAllRead">
          {{ t('notifications.markAll') }}
        </button>
      </div>
    </div>
    <div class="error" v-if="error">{{ error }}</div>
    <p class="muted">{{ t('notifications.hint') }}</p>

    <table v-if="rows.length">
      <tr>
        <th>{{ t('notifications.colWhat') }}</th>
        <th>{{ t('notifications.colWhen') }}</th>
        <th></th>
      </tr>
      <tr v-for="n in rows" :key="n.id" :class="{ unread: !n.read }">
        <td>
          <span class="dot" :class="{ on: !n.read }" :aria-label="n.read ? '' : t('notifications.unread')"></span>
          <a v-if="n.tid" :href="`/skinlib/${n.tid}`">{{ text(n) }}</a>
          <span v-else>{{ text(n) }}</span>
        </td>
        <td class="muted">{{ when(n.createdAt) }}</td>
        <td><button class="secondary small" v-if="!n.read" :disabled="busy" @click="markRead(n.id)">{{ t('notifications.markRead') }}</button></td>
      </tr>
    </table>
    <EmptyState v-else :text="onlyUnread ? t('notifications.emptyUnread') : t('notifications.empty')" />
    <Pager :page="page" :pages="pages" :total="total" @update:page="(p) => { page = p; load() }" />
  </div>
</template>

<style scoped>
.filter { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-s); color: var(--moon-2); }
.unread td { font-weight: 600; }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 8px; background: transparent; border: 1px solid var(--line); }
.dot.on { background: var(--cinnabar); border-color: var(--cinnabar); }
</style>
