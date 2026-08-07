<template>
  <div>
    <div class="flex flex-wrap justify-between items-center mb-8 gap-4">
      <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('nodes_title') }}<span class="font-mono text-indigo-400">]</span></h1>
      <div class="flex flex-col sm:flex-row flex-wrap items-stretch sm:items-center gap-3 w-full lg:w-auto">
        <div class="relative flex-1 min-w-[220px]">
          <Search class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-zinc-500 pointer-events-none" />
          <input v-model="searchQuery" type="text" :placeholder="t('nodes_search_ph')" :aria-label="t('nodes_search_aria')"
            class="w-full pl-10 pr-4 py-2 bg-zinc-900 border border-white/10 rounded-xl text-sm text-white placeholder-zinc-500 focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 transition-colors" />
        </div>
        <select v-model="statusFilter" :aria-label="t('nodes_filter_aria')"
          class="w-full sm:w-auto bg-zinc-900 border border-white/10 rounded-xl px-3 py-2 text-sm text-zinc-300 focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 transition-colors cursor-pointer">
          <option value="all">{{ t('nodes_filter_all') }}</option>
          <option value="online">{{ t('nodes_filter_online') }}</option>
          <option value="offline">{{ t('nodes_filter_offline') }}</option>
        </select>
        <button @click="refreshList" class="flex items-center justify-center space-x-2 px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-all duration-300" :title="t('nodes_refresh_list_tt')" :disabled="refreshingList">
          <RefreshCw :class="['w-5 h-5', refreshingList ? 'animate-spin' : '']" />
          <span class="font-mono">{{ refreshingList ? `[${t('nodes_refreshing')}]` : `[${t('nodes_refresh')}]` }}</span>
        </button>
      </div>
    </div>

    <div v-if="toast" :class="['fixed bottom-20 md:bottom-6 right-4 md:right-6 z-50 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl border', toastType === 'success' ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-200' : 'bg-red-500/15 border-red-500/40 text-red-200']">
      {{ toast }}
    </div>

    <div v-if="error" class="bg-red-900/20 border border-red-500/30 text-red-300 px-4 py-3 rounded-xl mb-6" role="alert">
      <strong class="font-bold">{{ t('client_error_label') }}:</strong>
      <span class="block sm:inline">{{ error }}</span>
    </div>

    <div v-if="filteredNodes.length > 0" class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 items-stretch">
      <div v-for="node in filteredNodes" :key="node.id" class="h-full"><NodeCard :node="node" @node-updated="fetchNodes" @node-deleted="onNodeDeleted" /></div>
    </div>

    <div v-else-if="nodes.length > 0" class="text-center py-16">
      <p class="text-zinc-500 text-lg">{{ t('nodes_no_match') }}</p>
    </div>

    <div v-else-if="!error" class="text-center py-16">
      <p class="text-zinc-500 text-lg">{{ t('nodes_no_nodes') }}</p>
    </div>

    <!-- FAB backdrop overlay (dims & dismisses when menu is open) -->
    <div v-if="fabOpen" :class="['fixed inset-0 z-40', fabBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click="fabOpen = false"></div>

    <!-- Floating Action Button -->
    <div class="fixed bottom-24 right-4 md:bottom-8 md:right-8 z-50 flex flex-col-reverse items-end gap-3">
      <transition name="slide-up">
        <div v-if="fabOpen" class="flex flex-col items-end gap-3">
          <button v-if="canPurgeNodes" @click="handlePurgeOffline"
            class="flex items-center space-x-2 px-4 py-2.5 rounded-full bg-red-500/10 hover:bg-red-500/20 border border-red-500/30 text-red-300 text-sm font-semibold shadow-lg shadow-red-500/10 transition-all duration-300 max-w-full"
            :title="t('nodes_purge_tt')">
            <Trash2 class="w-4 h-4 shrink-0" />
            <span class="font-mono truncate min-w-0">[{{ t('nodes_purge') }}]</span>
          </button>
          <button v-if="canEditSub" @click="handleRefreshAllSubs"
            class="flex items-center space-x-2 px-4 py-2.5 rounded-full bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 text-sm font-semibold shadow-lg shadow-indigo-500/10 transition-all duration-300 max-w-full"
            :title="t('nodes_refresh_subs_tt')" :disabled="refreshingSubs">
            <RefreshCw :class="['w-4 h-4 shrink-0', refreshingSubs ? 'animate-spin' : '']" />
            <span class="font-mono truncate min-w-0">{{ refreshingSubs ? `[${t('nodes_updating')}]` : `[${t('nodes_update_all')}]` }}</span>
          </button>
          <button v-if="canEditSub" @click="showMassUpdateModal = true"
            class="flex items-center space-x-2 px-4 py-2.5 rounded-full bg-emerald-500/15 hover:bg-emerald-500/25 border border-emerald-500/30 text-emerald-100 text-sm font-semibold shadow-lg shadow-emerald-500/10 transition-all duration-300 max-w-full"
            :title="t('nodes_mass_domain_tt')">
            <Link class="w-4 h-4 shrink-0" />
            <span class="font-mono truncate min-w-0">[{{ t('nodes_mass_domain_btn') }}]</span>
          </button>
        </div>
      </transition>
      <button @click="fabOpen = !fabOpen"
        class="px-5 py-3 bg-zinc-900/90 hover:bg-zinc-800 backdrop-blur-xl border border-white/10 hover:border-indigo-500/30 rounded-xl text-sm font-semibold font-mono shadow-2xl transition-all cursor-pointer flex items-center gap-2"
        :title="t('nodes_quick_actions_tt')" :aria-label="t('nodes_quick_actions_tt')">
        <span class="text-indigo-400 font-bold">[</span>
        <svg class="w-4 h-4 inline-block text-indigo-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"/></svg>
        <span class="text-zinc-200">{{ t('nodes_commands') }}</span>
        <span class="text-indigo-400 font-bold">]</span>
      </button>
    </div>

    <!-- Mass Update Subscription Domain Modal -->
    <div v-if="showMassUpdateModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showMassUpdateModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('nodes_mass_domain') }}<span class="font-mono text-indigo-400">]</span></h2>
        <p class="text-zinc-400 mb-4">{{ t('nodes_mass_domain_hint') }}</p>
        <div class="space-y-4">
          <div>
            <label for="mass_domain" class="block text-sm font-medium text-zinc-400">{{ t('nodes_new_domain') }}</label>
            <input v-model="massUpdateDomain" type="text" id="mass_domain" placeholder="sub.yourdomain.com" class="mt-1 block w-full bg-zinc-800/80 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50" required>
          </div>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showMassUpdateModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
          <button @click="handleMassUpdateDomain" class="px-4 py-2 bg-emerald-500/20 hover:bg-emerald-500/30 border border-emerald-500/30 text-emerald-100 rounded-xl transition-colors">{{ t('update_all') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, inject, onMounted, onUnmounted } from 'vue';
import axios from 'axios';
import NodeCard from './NodeCard.vue';
import { Link, Trash2, RefreshCw, Search } from 'lucide-vue-next';

const POLLING_INTERVAL = 5000; // 5 seconds
const ONLINE_THRESHOLD_SECONDS = 90;

export default {
  name: 'Nodes',
  components: {
    NodeCard,
    Link,
    Trash2,
    RefreshCw,
    Search,
  },
  setup() {
    const authCtx = inject('authCtx', {});
    const t = inject('t') || ((k) => k);
    const prefs = inject('prefs', ref({ theme_mode: 'obsidian' }));
    const modalBackdrop = computed(() => prefs.value.theme_mode === 'light' ? 'bg-zinc-900/25 backdrop-blur-sm' : 'bg-black/75 backdrop-blur-md');
    const fabBackdrop = computed(() => prefs.value.theme_mode === 'light' ? 'bg-zinc-900/20' : 'bg-black/75 backdrop-blur-md');
    const canEditSub = computed(() => authCtx.canEditSub?.value ?? false);
    const canPurgeNodes = computed(() => authCtx.canPurgeNodes?.value ?? false);
    const nodes = ref([]);
    const error = ref(null);
    const showMassUpdateModal = ref(false);
    const massUpdateDomain = ref('');
    const toast = ref('');
    const toastType = ref('success');
    const refreshingSubs = ref(false);
    const refreshingList = ref(false);
    const searchQuery = ref('');
    const statusFilter = ref('all');
    const fabOpen = ref(false);
    let pollInterval;
    let toastTimer;

    const isNodeOnline = (node) => {
      if (!node.last_seen) return false;
      const lastSeen = new Date(node.last_seen);
      const diffSeconds = (new Date() - lastSeen) / 1000;
      return diffSeconds < ONLINE_THRESHOLD_SECONDS;
    };

    const filteredNodes = computed(() => {
      let result = nodes.value;
      if (statusFilter.value !== 'all') {
        const wantOnline = statusFilter.value === 'online';
        result = result.filter((n) => isNodeOnline(n) === wantOnline);
      }
      const query = searchQuery.value.trim().toLowerCase();
      if (query) {
        result = result.filter((n) =>
          (n.name || '').toLowerCase().includes(query) ||
          (n.hostname || '').toLowerCase().includes(query) ||
          (n.ip_lan || '').toLowerCase().includes(query)
        );
      }
      return result;
    });

    const showToast = (msg, type = 'success', duration = 4000) => {
      toast.value = msg;
      toastType.value = type;
      clearTimeout(toastTimer);
      toastTimer = setTimeout(() => { toast.value = ''; }, duration);
    };

    const handleRefreshAllSubs = async () => {
      if (!confirm(t('nodes_confirm_refresh_all'))) return;
      refreshingSubs.value = true;
      try {
        const response = await axios.post('/api/web/nodes/mass-update-sub', {});
        if (response.data.status !== 'ok') throw new Error('Refresh failed');
        showToast(t('nodes_queued_refresh', { n: response.data.commands_queued }));
        fetchNodes();
      } catch (e) {
        console.error("Failed to refresh all subscriptions:", e);
        showToast(t('nodes_failed_refresh'), 'error');
      } finally {
        refreshingSubs.value = false;
      }
    };

    const refreshList = async () => {
      refreshingList.value = true;
      try {
        await fetchNodes();
      } finally {
        refreshingList.value = false;
      }
    };

    const fetchNodes = async () => {
      try {
        const response = await axios.get('/api/web/nodes');
        nodes.value = response.data || [];
        error.value = null;
      } catch (e) {
        console.error("Failed to fetch nodes:", e);
        error.value = e.message;
      }
    };

    const onNodeDeleted = (nodeId) => {
      nodes.value = nodes.value.filter(n => n.id !== nodeId);
    };

    const handlePurgeOffline = async () => {
      if (!confirm(t('nodes_confirm_purge'))) return;
      try {
        const response = await axios.post('/api/web/nodes/purge-offline');
        if (response.data.status !== 'ok') throw new Error('Purge failed');
        alert(t('nodes_purged', { n: response.data.deleted }));
        fetchNodes();
      } catch (e) {
        console.error("Failed to purge offline nodes:", e);
        error.value = e.message;
        alert(t('nodes_purge_failed'));
      }
    };

    const handleMassUpdateDomain = async () => {
      if (!massUpdateDomain.value) {
        alert(t('nodes_domain_required'));
        return;
      }
      if (!confirm(t('nodes_confirm_mass_domain', { domain: massUpdateDomain.value }))) return;
      try {
        const response = await axios.post('/api/web/devices/mass-update-domain', {
          domain: massUpdateDomain.value,
        });
        if (response.data.status !== 'ok') throw new Error('Failed to mass update domain');
        alert(t('nodes_mass_domain_result', { updated: response.data.nodes_updated, total: response.data.nodes_total }));
        showMassUpdateModal.value = false;
        massUpdateDomain.value = '';
        fetchNodes();
      } catch (error) {
        console.error('Error mass updating domain:', error);
        alert(t('nodes_mass_domain_failed'));
      }
    };

    onMounted(() => {
      fetchNodes();
      pollInterval = setInterval(fetchNodes, POLLING_INTERVAL);
    });

    onUnmounted(() => {
      clearInterval(pollInterval);
    });

return {
      nodes,
      filteredNodes,
      error,
      canEditSub,
      canPurgeNodes,
      showMassUpdateModal,
      massUpdateDomain,
      toast,
      toastType,
      searchQuery,
      statusFilter,
      fabOpen,
      fetchNodes,
      refreshList,
      refreshingList,
      handleRefreshAllSubs,
      refreshingSubs,
      handleMassUpdateDomain,
      onNodeDeleted,
      handlePurgeOffline,
      t,
};
  },
};
</script>

<style scoped>
.slide-up-enter-active {
  transition: all 0.3s ease-out;
  opacity: 1;
  transform: translateY(0);
}
.slide-up-leave-active {
  transition: all 0.2s ease-in;
}
.slide-up-enter-from {
  opacity: 0;
  transform: translateY(16px);
}
.slide-up-leave-to {
  opacity: 0;
  transform: translateY(16px);
  pointer-events: none;
}
</style>
