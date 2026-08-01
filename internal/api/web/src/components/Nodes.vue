<template>
  <div>
    <div class="flex justify-between items-center mb-8">
      <h1 class="text-4xl font-bold">Nodes</h1>
      <div class="flex space-x-4">
        <button @click="showMassUpdateModal = true" class="flex items-center space-x-2 px-4 py-2 bg-green-600 hover:bg-green-700 rounded-lg transition-colors">
          <Link class="w-5 h-5" />
          <span>Mass Update Subscription Domain</span>
        </button>
        <button @click="handleUpdateAll" class="flex items-center space-x-2 px-4 py-2 bg-indigo-600 hover:bg-indigo-700 rounded-lg transition-colors">
          <RefreshCw class="w-5 h-5" />
          <span>Update All Devices</span>
        </button>
        <button @click="fetchNodes" class="flex items-center space-x-2 px-4 py-2 bg-gray-700 hover:bg-gray-600 rounded-lg transition-colors" title="Refresh list">
          <Activity class="w-5 h-5" />
        </button>
      </div>
    </div>

    <div v-if="error" class="bg-red-900/50 border border-red-500 text-red-300 px-4 py-3 rounded-lg mb-6" role="alert">
      <strong class="font-bold">Error:</strong>
      <span class="block sm:inline">{{ error }}</span>
    </div>

    <div v-if="nodes.length > 0" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-6">
      <NodeCard v-for="node in nodes" :key="node.id" :node="node" @node-updated="fetchNodes" @node-deleted="onNodeDeleted" />
    </div>

    <div v-else-if="!error" class="text-center py-16">
      <p class="text-gray-500 text-lg">No nodes found. Waiting for agents to poll...</p>
    </div>

    <!-- Mass Update Subscription Domain Modal -->
    <div v-if="showMassUpdateModal" class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div class="bg-gray-800 rounded-lg p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6">Mass Update Subscription Domain</h2>
        <p class="text-gray-400 mb-4">This will replace the domain portion of the subscription URL for ALL nodes while preserving each node&apos;s unique path and token.</p>
        <div class="space-y-4">
          <div>
            <label for="mass_domain" class="block text-sm font-medium text-gray-400">New Subdomain</label>
            <input v-model="massUpdateDomain" type="text" id="mass_domain" placeholder="sub2.malaxis.ru" class="mt-1 block w-full bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500" required>
          </div>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showMassUpdateModal = false" class="px-4 py-2 bg-gray-600 hover:bg-gray-700 rounded-lg transition-colors">Cancel</button>
          <button @click="handleMassUpdateDomain" class="px-4 py-2 bg-green-600 hover:bg-green-700 rounded-lg transition-colors">Update All</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, onMounted, onUnmounted } from 'vue';
import axios from 'axios';
import NodeCard from './NodeCard.vue';
import { RefreshCw, Activity, Link } from 'lucide-vue-next';

const POLLING_INTERVAL = 5000; // 5 seconds

export default {
  name: 'Nodes',
  components: {
    NodeCard,
    RefreshCw,
    Activity,
    Link,
  },
  setup() {
    const nodes = ref([]);
    const error = ref(null);
    const showMassUpdateModal = ref(false);
    const massUpdateDomain = ref('');
    let pollInterval;

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

    const handleUpdateAll = async () => {
      try {
        const subUrl = prompt('Enter subscription URL for all nodes:');
        if (!subUrl) return;
        if (!confirm(`Update ALL nodes with subscription URL?\n${subUrl}`)) return;
        const response = await axios.post('/api/web/devices/mass-update-sub', { sub_url: subUrl });
        if (response.data.status !== 'ok') throw new Error('Mass update failed');
        alert(`Updated ${response.data.nodes_updated} nodes, ${response.data.commands_queued} commands queued.`);
        fetchNodes();
      } catch (e) {
        console.error("Failed to update all devices:", e);
        error.value = e.message;
        alert('Could not update all devices.');
      }
    };

    const onNodeDeleted = (nodeId) => {
      nodes.value = nodes.value.filter(n => n.id !== nodeId);
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
      showMassUpdateModal,
      massUpdateDomain,
      fetchNodes,
      handleUpdateAll,
      handleMassUpdateDomain,
      onNodeDeleted,
    };
  },
};
</script>
