<template>
  <div>
    <div class="flex flex-wrap justify-between items-center mb-8 gap-4">
      <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>Nodes<span class="font-mono text-indigo-400">]</span></h1>
      <div class="flex flex-wrap items-center gap-3">
        <div class="relative">
          <Search class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-zinc-500 pointer-events-none" />
          <input v-model="searchQuery" type="text" placeholder="Search name, hostname, IP..." aria-label="Search nodes"
            class="w-64 pl-10 pr-4 py-2 bg-zinc-900 border border-white/10 rounded-xl text-sm text-white placeholder-zinc-500 focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 transition-colors" />
        </div>
        <select v-model="statusFilter" aria-label="Filter by status"
          class="bg-zinc-900 border border-white/10 rounded-xl px-3 py-2 text-sm text-zinc-300 focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 transition-colors cursor-pointer">
          <option value="all">All Nodes</option>
          <option value="online">Online Only</option>
          <option value="offline">Offline Only</option>
        </select>
        <button @click="refreshList" class="flex items-center space-x-2 px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-all duration-300" title="Refresh Nodes List" :disabled="refreshingList">
          <RefreshCw :class="['w-5 h-5', refreshingList ? 'animate-spin' : '']" />
          <span class="font-mono">{{ refreshingList ? '[Refreshing...]' : '[Refresh List]' }}</span>
        </button>
      </div>
    </div>

    <div v-if="toast" :class="['fixed bottom-6 right-6 z-50 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl border', toastType === 'success' ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-200' : 'bg-red-500/15 border-red-500/40 text-red-200']">
      {{ toast }}
    </div>

    <div v-if="error" class="bg-red-900/20 border border-red-500/30 text-red-300 px-4 py-3 rounded-xl mb-6" role="alert">
      <strong class="font-bold">Error:</strong>
      <span class="block sm:inline">{{ error }}</span>
    </div>

    <div v-if="filteredNodes.length > 0" class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-6 items-stretch">
      <div v-for="node in filteredNodes" :key="node.id" class="h-full"><NodeCard :node="node" @node-updated="fetchNodes" @node-deleted="onNodeDeleted" /></div>
    </div>

    <div v-else-if="nodes.length > 0" class="text-center py-16">
      <p class="text-zinc-500 text-lg">No nodes match your search or filters.</p>
    </div>

    <div v-else-if="!error" class="text-center py-16">
      <p class="text-zinc-500 text-lg">No nodes found. Waiting for agents to poll...</p>
    </div>

    <!-- Floating Action Button -->
    <div class="fixed bottom-8 right-8 z-50 flex flex-col-reverse items-end gap-3">
      <transition name="slide-up">
        <div v-if="fabOpen" class="flex flex-col items-end gap-3">
          <button v-if="canEditSub" @click="handlePurgeOffline"
            class="flex items-center space-x-2 px-4 py-2.5 rounded-full bg-red-500/10 hover:bg-red-500/20 border border-red-500/30 text-red-300 text-sm font-semibold shadow-lg shadow-red-500/10 transition-all duration-300"
            title="Delete ghost nodes offline for more than 7 days">
            <Trash2 class="w-4 h-4" />
            <span class="font-mono">[Purge Offline]</span>
          </button>
          <button v-if="canEditSub" @click="handleRefreshAllSubs"
            class="flex items-center space-x-2 px-4 py-2.5 rounded-full bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 text-sm font-semibold shadow-lg shadow-indigo-500/10 transition-all duration-300"
            title="Queue a subscription re-fetch for ALL nodes" :disabled="refreshingSubs">
            <RefreshCw :class="['w-4 h-4', refreshingSubs ? 'animate-spin' : '']" />
            <span class="font-mono">{{ refreshingSubs ? '[Updating...]' : '[Update All Devices]' }}</span>
          </button>
          <button v-if="canEditSub" @click="showMassUpdateModal = true"
            class="flex items-center space-x-2 px-4 py-2.5 rounded-full bg-emerald-500/15 hover:bg-emerald-500/25 border border-emerald-500/30 text-emerald-100 text-sm font-semibold shadow-lg shadow-emerald-500/10 transition-all duration-300"
            title="Mass update the subscription domain for all nodes">
            <Link class="w-4 h-4" />
            <span class="font-mono">[Mass Update Domain]</span>
          </button>
        </div>
      </transition>
      <button @click="fabOpen = !fabOpen"
        class="px-3.5 py-2 bg-zinc-900/90 hover:bg-zinc-800 backdrop-blur-xl border border-white/10 hover:border-indigo-500/30 rounded-xl text-xs font-mono shadow-2xl transition-all cursor-pointer flex items-center gap-1.5"
        title="Quick Fleet Actions" aria-label="Quick Fleet Actions">
        <span class="text-indigo-400 font-bold">[</span>
        <svg class="w-3.5 h-3.5 text-indigo-400 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"/></svg>
        <span class="text-zinc-200 font-medium">Commands</span>
        <span class="text-indigo-400 font-bold">]</span>
      </button>
    </div>

    <!-- Mass Update Subscription Domain Modal -->
    <div v-if="showMassUpdateModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="showMassUpdateModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>Mass Update Subscription Domain<span class="font-mono text-indigo-400">]</span></h2>
        <p class="text-zinc-400 mb-4">This will replace the domain portion of the subscription URL for ALL nodes while preserving each node&apos;s unique path and token.</p>
        <div class="space-y-4">
          <div>
            <label for="mass_domain" class="block text-sm font-medium text-zinc-400">New Subdomain</label>
            <input v-model="massUpdateDomain" type="text" id="mass_domain" placeholder="sub2.malaxis.ru" class="mt-1 block w-full bg-zinc-800/80 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50" required>
          </div>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showMassUpdateModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button @click="handleMassUpdateDomain" class="px-4 py-2 bg-emerald-500/20 hover:bg-emerald-500/30 border border-emerald-500/30 text-emerald-100 rounded-xl transition-colors">Update All</button>
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
    const canEditSub = computed(() => authCtx.canEditSub?.value ?? false);
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
      if (!confirm('Queue subscription refresh for ALL nodes? Each agent will re-fetch its subscription and re-apply VPN config.')) return;
      refreshingSubs.value = true;
      try {
        const response = await axios.post('/api/web/nodes/mass-update-sub', {});
        if (response.data.status !== 'ok') throw new Error('Refresh failed');
        showToast(`Queued subscription refresh for ${response.data.commands_queued} node(s).`);
        fetchNodes();
      } catch (e) {
        console.error("Failed to refresh all subscriptions:", e);
        showToast('Could not queue subscription refresh.', 'error');
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
      if (!confirm('Delete ALL nodes that have been offline for more than 7 days?')) return;
      try {
        const response = await axios.post('/api/web/nodes/purge-offline');
        if (response.data.status !== 'ok') throw new Error('Purge failed');
        alert(`Purged ${response.data.deleted} offline node(s).`);
        fetchNodes();
      } catch (e) {
        console.error("Failed to purge offline nodes:", e);
        error.value = e.message;
        alert('Could not purge offline nodes.');
      }
    };

    const handleMassUpdateDomain = async () => {
      if (!massUpdateDomain.value) {
        alert('Please enter a new subdomain.');
        return;
      }
      if (!confirm(`Replace subscription domain for ALL nodes with:\n${massUpdateDomain.value}`)) return;
      try {
        const response = await axios.post('/api/web/devices/mass-update-domain', {
          domain: massUpdateDomain.value,
        });
        if (response.data.status !== 'ok') throw new Error('Failed to mass update domain');
        alert(`Subscription domain updated for ${response.data.nodes_updated} out of ${response.data.nodes_total} nodes.`);
        showMassUpdateModal.value = false;
        massUpdateDomain.value = '';
        fetchNodes();
      } catch (error) {
        console.error('Error mass updating domain:', error);
        alert('Could not mass update subscription domain.');
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
