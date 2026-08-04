<template>
  <div>
    <div class="flex flex-wrap justify-between items-center mb-8 gap-4">
      <h1 class="text-4xl font-bold tracking-tight">Nodes</h1>
      <div class="flex flex-wrap space-x-4 gap-y-2">
        <button v-if="canEditSub" @click="showMassUpdateModal = true" class="flex items-center space-x-2 px-4 py-2 bg-emerald-500/15 hover:bg-emerald-500/25 border border-emerald-500/30 text-emerald-100 rounded-xl transition-all duration-300">
          <Link class="w-5 h-5" />
          <span>Mass Update Subscription Domain</span>
        </button>
        <button v-if="canEditSub" @click="handleRefreshAllSubs" class="flex items-center space-x-2 px-4 py-2 bg-sky-500/15 hover:bg-sky-500/25 border border-sky-500/30 text-sky-100 rounded-xl transition-all duration-300" title="Queue a subscription re-fetch for ALL nodes" :disabled="refreshingSubs">
          <RefreshCw :class="['w-5 h-5', refreshingSubs ? 'animate-spin' : '']" />
          <span>{{ refreshingSubs ? 'Refreshing...' : 'Refresh All Subscriptions' }}</span>
        </button>
        <button v-if="canEditSub" @click="handlePurgeOffline" class="flex items-center space-x-2 px-4 py-2 bg-red-500/10 hover:bg-red-500/20 border border-red-500/20 text-red-300 rounded-xl transition-all duration-300" title="Delete ghost nodes offline for more than 7 days">
          <Trash2 class="w-5 h-5" />
          <span>Purge Offline</span>
        </button>
        <button @click="fetchNodes" class="flex items-center space-x-2 px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-all duration-300" title="Refresh list">
          <Activity class="w-5 h-5" />
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

    <div v-if="nodes.length > 0" class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-6">
      <NodeCard v-for="node in nodes" :key="node.id" :node="node" @node-updated="fetchNodes" @node-deleted="onNodeDeleted" />
    </div>

    <div v-else-if="!error" class="text-center py-16">
      <p class="text-zinc-500 text-lg">No nodes found. Waiting for agents to poll...</p>
    </div>

    <!-- Mass Update Subscription Domain Modal -->
    <div v-if="showMassUpdateModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4">
      <div class="bg-zinc-900/90 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Mass Update Subscription Domain</h2>
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
import { Activity, Link, Trash2, RefreshCw } from 'lucide-vue-next';

const POLLING_INTERVAL = 5000; // 5 seconds

export default {
  name: 'Nodes',
  components: {
    NodeCard,
    Activity,
    Link,
    Trash2,
    RefreshCw,
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
    let pollInterval;
    let toastTimer;

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
      error,
      canEditSub,
      showMassUpdateModal,
      massUpdateDomain,
      toast,
      toastType,
      fetchNodes,
      handleRefreshAllSubs,
      refreshingSubs,
      handleMassUpdateDomain,
      onNodeDeleted,
      handlePurgeOffline,
    };
  },
};
</script>
