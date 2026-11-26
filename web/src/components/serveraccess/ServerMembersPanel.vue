<script setup>
import { shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { computed } from 'vue'
import EmptyState from '../EmptyState.vue'
import Pager from '../Pager.vue'

const props = defineProps({
  members: { type: Array, default: () => [] },
  invites: { type: Array, default: () => [] },
  busy: Boolean,
  canManageMembers: Boolean,
  inviteToken: String,
  page: { type: Number, default: 1 },
  size: { type: Number, default: 50 },
  total: { type: Number, default: 0 },
})
const emit = defineEmits(['invite', 'remove-member', 'revoke-invite', 'hide-invite', 'update:page'])
const { t } = useI18n()
const email = shallowRef('')
const role = shallowRef('viewer')
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.size)))
function invite() {
  if (!props.canManageMembers || props.busy || !email.value.trim()) return
  emit('invite', email.value.trim(), role.value)
  email.value = ''
}
function roleLabel(value) {
  const key = `serverAccess.roleMap.${value}`
  const label = t(key)
  return label === key ? value : label
}
</script>

<template>
  <section class="card members-panel">
    <h2>{{ t('serverAccess.members') }}</h2>
    <form v-if="canManageMembers" class="row invite-row" @submit.prevent="invite">
      <label class="sr-only" for="sa-invite-email">{{ t('serverAccess.inviteEmail') }}</label>
      <input id="sa-invite-email" v-model="email" type="email" maxlength="191" :placeholder="t('serverAccess.inviteEmail')" required />
      <label class="sr-only" for="sa-invite-role">{{ t('serverAccess.inviteRole') }}</label>
      <select id="sa-invite-role" v-model="role"><option value="admin">{{ roleLabel('admin') }}</option><option value="viewer">{{ roleLabel('viewer') }}</option></select>
      <button :disabled="busy || !email.trim()">{{ t('serverAccess.invite') }}</button>
    </form>
    <div v-if="inviteToken" class="success token-box" role="status">
      <strong>{{ t('serverAccess.inviteToken') }}</strong><code>{{ inviteToken }}</code>
      <p>{{ t('serverAccess.inviteTokenHint') }}</p>
      <button type="button" class="secondary small" @click="emit('hide-invite')">{{ t('serverAccess.hide') }}</button>
    </div>
    <h3>{{ t('serverAccess.members') }}</h3>
    <EmptyState v-if="!members.length" :text="t('serverAccess.noMembers')" />
    <div v-else class="table-scroll">
      <table>
        <caption class="sr-only">{{ t('serverAccess.members') }}</caption>
        <thead><tr><th scope="col">UID</th><th scope="col">{{ t('serverAccess.member') }}</th><th scope="col">{{ t('serverAccess.inviteRole') }}</th><th scope="col">{{ t('serverAccess.createdAt') }}</th><th scope="col">{{ t('serverAccess.actions') }}</th></tr></thead>
        <tbody><tr v-for="member in members" :key="member.uid">
          <td>{{ member.uid }}</td><td>{{ member.nickname || '—' }}</td><td>{{ roleLabel(member.role) }}</td><td>{{ member.createdAt }}</td>
          <td><button v-if="canManageMembers" class="secondary small danger-outline" :disabled="busy" @click="emit('remove-member', member.uid)">{{ t('serverAccess.removeMember') }}</button></td>
        </tr></tbody>
      </table>
    </div>
    <template v-if="canManageMembers">
      <h3>{{ t('serverAccess.invitations') }}</h3>
      <EmptyState v-if="!invites.length" :text="t('serverAccess.noInvites')" />
      <div v-else class="table-scroll">
        <table>
          <caption class="sr-only">{{ t('serverAccess.invitations') }}</caption>
          <thead><tr><th scope="col">{{ t('serverAccess.inviteEmail') }}</th><th scope="col">{{ t('serverAccess.inviteRole') }}</th><th scope="col">{{ t('serverAccess.expiresAt') }}</th><th scope="col">{{ t('serverAccess.actions') }}</th></tr></thead>
          <tbody><tr v-for="invitation in invites" :key="invitation.id">
            <td>{{ invitation.email }}</td><td>{{ roleLabel(invitation.role) }}</td><td>{{ invitation.expiresAt }}</td>
            <td><button class="secondary small danger-outline" :disabled="busy" @click="emit('revoke-invite', invitation.id)">{{ t('serverAccess.revokeInvite') }}</button></td>
          </tr></tbody>
        </table>
      </div>
      <Pager :page="page" :pages="pages" :total="total" @update:page="emit('update:page', $event)" />
    </template>
  </section>
</template>

<style scoped>
.members-panel { display: grid; gap: var(--sp-3); }
.members-panel h2, .members-panel h3 { margin: 0; font-size: var(--fs-m); }
.members-panel table { min-width: 640px; }
.invite-row { margin: 0; flex-wrap: wrap; }
.invite-row input { flex: 1; min-width: 0; }
.token-box { display: grid; gap: var(--sp-2); }
.token-box code { overflow-wrap: anywhere; }
.token-box p { margin: 0; }
.token-box button { justify-self: start; }
</style>
