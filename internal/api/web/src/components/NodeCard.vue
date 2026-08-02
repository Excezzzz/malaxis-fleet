<template>
  <div class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl p-5 flex flex-col justify-between transition-all duration-300 hover:border-indigo-500/30 hover:bg-zinc-900/60 shadow-lg shadow-black/20">
    <div>
      <div class="flex justify-between items-start mb-4">
        <div class="flex items-center space-x-3">
          <div class="p-2 rounded-xl bg-white/5 border border-white/10">
            <component :is="nodeIcon" class="w-6 h-6 text-indigo-300" />
          </div>
          <h2 class="text-xl font-bold tracking-tight">{{ node.name }}</h2>
        </div>
        <div class="flex items-center space-x-2">
            <div :class="['w-3 h-3 rounded-full', isOnline ? 'bg-green-500 animate-pulse shadow-[0_0_10px_rgba(34,197,94,0.6)]' : 'bg-red-500 shadow-[0_0_10px_rgba(239,68,68,0.4)]']" :title="isOnline ? 'Online' : 'Offline'"></div>
        </div>
      </div>
      <div class="space-y-2 text-sm text-zinc-300">
        <p><strong class="text-zinc-500 font-medium">LAN IP:</strong> {{ node.ip_lan || 'N/A' }}</p>
        <p><strong class="text-zinc-500 font-medium">Hostname:</strong> {{ node.hostname || 'N/A' }}</p>
        <p><strong class="text-zinc-500 font-medium">VPN Server:</strong> {{ node.active_server || 'None' }} <span v-if="node.active_engine" class="text-xs text-zinc-500">({{ node.active_engine }}{{ node.active_proto ? ' / ' + node.active_proto : '' }})</span></p>
        <p><strong class="text-zinc-500 font-medium">Sub URL:</strong> <span class="text-xs text-zinc-400 truncate max-w-[200px] inline-block align-bottom">{{ node.sub_url || 'Not set' }}</span></p>
        <p><strong class="text-zinc-500 font-medium">Last Seen:</strong> {{ timeSince(node.last_seen) }}</p>
      </div>
      <div v-if="node.pipeline_status" class="mt-3 text-sm">
        <p class="flex items-center space-x-2">
            <component :is="pipelineStatusIcon(node.pipeline_status)" class="w-4 h-4 text-emerald-400" />
            <strong class="text-zinc-500 font-medium">Status:</strong>
            <span>{{ node.pipeline_status }}</span>
        </p>
        <p v-if="node.status_message" class="text-xs text-zinc-400 pl-6">{{ node.status_message }}</p>
      </div>
    </div>
    <div class="mt-5 space-y-2">
      <button @click="showSubModal = true" class="w-full flex items-center justify-center space-x-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 font-semibold py-2 px-4 rounded-xl transition-all duration-300">
        <Link class="w-4 h-4" />
        <span>Manage Subscription URL</span>
      </button>
      <button @click="switchVpnProfile" class="w-full flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 rounded-xl transition-all duration-300">
        <Shield class="w-4 h-4" />
        <span>Switch VPN Configuration</span>
      </button>
      <button @click="confirmDeleteNode" class="w-full flex items-center justify-center space-x-2 bg-red-500/10 hover:bg-red-500/20 border border-red-500/20 text-red-300 font-semibold py-2 px-4 rounded-xl transition-all duration-300">
        <Trash2 class="w-4 h-4" />
        <span>Delete Node</span>
      </button>
    </div>

    <!-- Subscription URL Modal -->
    <div v-if="showSubModal" class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50">
      <div class="bg-zinc-900/90 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Manage Subscription URL</h2>
        <div class="space-y-4">
          <div>
            <label class="block text-sm font-medium text-zinc-400">Current Subscription URL</label>
            <p class="mt-1 text-sm text-zinc-300 break-all">{{ node.sub_url || 'Not configured' }}</p>
          </div>
          <div>
            <label for="sub_url" class="block text-sm font-medium text-zinc-400">New Subscription URL</label>
            <input v-model="newSubUrl" type="text" id="sub_url" placeholder="https://example.com/subscription" class="mt-1 block w-full bg-zinc-800/80 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
          </div>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showSubModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="updateSubUrl" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Update URL</button>
        </div>
      </div>
    </div>

    <!-- Switch VPN Modal -->
    <div v-if="showSwitchModal" class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50">
      <div class="bg-zinc-900/90 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-2 tracking-tight">Switch VPN Configuration</h2>
        <p class="text-zinc-400 mb-5">Currently active server: <strong>{{ node.active_server || 'None' }}</strong></p>
        <div class="grid grid-cols-2 gap-3 mb-4">
          <button @click="switchTo('fastest')" class="flex items-center justify-center space-x-2 px-4 py-3 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors font-semibold">
            <span>⚡</span><span>Fastest</span>
          </button>
          <button @click="switchTo('balanced')" class="flex items-center justify-center space-x-2 px-4 py-3 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors font-semibold">
            <span>⚖️</span><span>Balanced</span>
          </button>
        </div>
        <p class="text-xs text-zinc-500 mb-2">Available servers ({{ (node.available_servers || []).length }})</p>
        <div v-if="(node.available_servers || []).length" class="grid grid-cols-2 gap-3 max-h-56 overflow-y-auto pr-1">
          <button v-for="srv in node.available_servers" :key="srv" @click="switchTo(srv)"
            :class="['px-4 py-3 rounded-xl transition-colors font-semibold truncate text-left', srv === node.active_server ? 'bg-emerald-500/20 hover:bg-emerald-500/30 border border-emerald-500/40 text-emerald-100' : 'bg-white/5 hover:bg-white/10 border border-white/10']">
            {{ srv }}
          </button>
        </div>
        <p v-else class="text-sm text-zinc-500">No server list reported yet — try again in a few seconds.</p>
        <div class="mt-8 flex justify-end">
          <button type="button" @click="showSwitchModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Close</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed } from 'vue';
import { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Trash2 } from 'lucide-vue-next';

const ONLINE_THRESHOLD_SECONDS = 90;

export default {
  name: 'NodeCard',
  components: { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Trash2 },
  props: {
    node: {
      type: Object,
      required: true,
    },
  },
  emits: ['node-updated', 'node-deleted'],
  setup(props, { emit }) {
    const showSubModal = ref(false);
    const newSubUrl = ref('');

    const isOnline = computed(() => {
      if (!props.node.last_seen) return false;
      const lastSeen = new Date(props.node.last_seen);
      const diffSeconds = (new Date() - lastSeen) / 1000;
      return diffSeconds < ONLINE_THRESHOLD_SECONDS;
    });

    const nodeIcon = computed(() => {
        const type = props.node.device_type ? props.node.device_type.toLowerCase() : '';
        if (type.includes('server')) {
            return 'Server';
        }
        return 'Cpu';
    });

    const pipelineStatusIcon = (status) => {
        switch (status) {
            case 'Queued': return 'Hourglass';
            case 'Fetched': return 'ArrowDown';
            case 'Engine Restarting': return 'Cog';
            case 'Verified & Active': return 'CheckCircle2';
            case 'Rollback Executed': return 'XCircle';
            default: return 'Hourglass';
        }
    };

    const timeSince = (dateStr) => {
        if (!dateStr) return 'never';
        const date = new Date(dateStr);
        const seconds = Math.floor((new Date() - date) / 1000);
        
        if (seconds < 5) return "just now";
        let interval = seconds / 31536000;
        if (interval > 1) return Math.floor(interval) + " years ago";
        interval = seconds / 2592000;
        if (interval > 1) return Math.floor(interval) + " months ago";
        interval = seconds / 86400;
        if (interval > 1) return Math.floor(interval) + " days ago";
        interval = seconds / 3600;
        if (interval > 1) return Math.floor(interval) + " hours ago";
        interval = seconds / 60;
        if (interval > 1) return Math.floor(interval) + " minutes ago";
        return Math.floor(seconds) + " seconds ago";
    };

    const updateSubUrl = async () => {
      if (!newSubUrl.value) {
        alert('Please enter a subscription URL.');
        return;
      }
      try {
        const response = await fetch(`/api/web/nodes/${props.node.id}/sub`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ sub_url: newSubUrl.value }),
        });
        if (!response.ok) throw new Error('Failed to update subscription URL');
        showSubModal.value = false;
        newSubUrl.value = '';
        alert('Subscription URL updated! The node will fetch it on the next poll.');
        emit('node-updated');
      } catch (error) {
        console.error('Error updating subscription URL:', error);
        alert('Could not update subscription URL.');
      }
    };

    const confirmDeleteNode = () => {
      if (confirm(`Delete this node "${props.node.name}" from fleet?`)) {
        deleteNode();
      }
    };

    const deleteNode = async () => {
      try {
        const response = await fetch(`/api/web/devices/${props.node.id}`, {
          method: 'DELETE',
        });
        if (!response.ok) throw new Error('Failed to delete node');
        alert('Node deleted successfully!');
        emit('node-deleted', props.node.id);
      } catch (error) {
        console.error('Error deleting node:', error);
        alert('Could not delete node.');
      }
    };

    const showSwitchModal = ref(false);

    const switchVpnProfile = () => {
      showSwitchModal.value = true;
    };

    const switchTo = async (target) => {
      try {
        const response = await fetch(`/api/web/devices/${props.node.id}/command`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ action: 'switch', outbound_tag: target }),
        });
        if (!response.ok) throw new Error('Failed to send switch command');
        showSwitchModal.value = false;
        emit('node-updated');
      } catch (error) {
        console.error('Error switching VPN:', error);
        alert('Could not send switch command.');
      }
    };

    return {
      isOnline,
      nodeIcon,
      pipelineStatusIcon,
      timeSince,
      showSubModal,
      newSubUrl,
      updateSubUrl,
      confirmDeleteNode,
      deleteNode,
      switchVpnProfile,
      showSwitchModal,
      switchTo,
    };
  },
};
</script>
