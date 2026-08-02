<template>
  <div :class="['bg-zinc-900 border rounded-2xl p-5 flex flex-col justify-between transition-colors duration-300 hover:border-indigo-500/30 hover:bg-zinc-800/80', isTerminated ? 'border-red-500/50 bg-red-950/20' : 'border-white/10']">
    <div>
      <div class="flex justify-between items-start mb-4">
        <div class="flex items-center space-x-3">
          <div class="p-2 rounded-xl bg-white/5 border border-white/10">
            <component :is="nodeIcon" class="w-6 h-6 text-indigo-300" />
          </div>
          <div class="flex items-center space-x-2">
            <h2 class="text-xl font-bold tracking-tight">{{ node.name }}</h2>
            <button v-if="canEditSub" @click="showRenameModal = true" title="Rename node"
              class="p-1.5 rounded-lg bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-400 hover:text-white transition-colors">
              <Pencil class="w-3.5 h-3.5" />
            </button>
            <span v-if="isTerminated" class="text-xs font-bold uppercase tracking-wider px-2 py-0.5 rounded-lg bg-red-500/20 border border-red-500/40 text-red-300">Terminated</span>
          </div>
        </div>
        <div class="flex items-center space-x-2">
            <div :class="['w-3 h-3 rounded-full', isOnline ? 'bg-green-500 animate-pulse' : 'bg-red-500']" :title="isOnline ? 'Online' : 'Offline'"></div>
        </div>
      </div>
      <div class="space-y-2.5 text-sm">
        <p class="flex items-baseline justify-between gap-2">
          <span class="text-zinc-500 text-xs uppercase tracking-wider shrink-0">LAN IP</span>
          <span class="text-white font-medium truncate">{{ node.ip_lan || 'N/A' }}</span>
        </p>
        <p class="flex items-baseline justify-between gap-2">
          <span class="text-zinc-500 text-xs uppercase tracking-wider shrink-0">Hostname</span>
          <span class="text-white font-medium truncate">{{ node.hostname || 'N/A' }}</span>
        </p>
        <p class="flex items-baseline justify-between gap-2">
          <span class="text-zinc-500 text-xs uppercase tracking-wider shrink-0">VPN Server</span>
          <span class="text-white font-medium truncate">{{ node.active_server || 'None' }}<span v-if="node.active_engine" class="text-xs text-zinc-500"> ({{ node.active_engine }}{{ node.active_proto ? ' / ' + node.active_proto : '' }})</span></span>
        </p>
        <p class="flex items-center justify-between gap-2">
          <span class="text-zinc-500 text-xs uppercase tracking-wider shrink-0">Sub URL</span>
          <template v-if="node.sub_url">
            <span class="text-xs text-zinc-400 truncate max-w-[200px] inline-block align-bottom" :title="node.sub_url">{{ node.sub_url }}</span>
            <button @click="copySubUrl" title="Copy subscription URL"
              class="p-1 rounded-md bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-400 hover:text-white transition-colors shrink-0">
              <Copy class="w-3 h-3" />
            </button>
          </template>
          <span v-else class="text-xs text-zinc-500">Not set</span>
        </p>
        <p class="flex items-baseline justify-between gap-2">
          <span class="text-zinc-500 text-xs uppercase tracking-wider shrink-0">Last Seen</span>
          <span class="text-white font-medium truncate" v-show="!isOnline">{{ timeSince(node.last_seen) }}</span>
          <span class="text-white font-medium truncate invisible" v-show="isOnline">{{ timeSince(node.last_seen) }}</span>
        </p>
      </div>
      <div v-if="node.pipeline_status" class="mt-3 text-sm">
        <p class="flex items-center space-x-2">
            <component :is="pipelineStatusIcon(node.pipeline_status)" class="w-4 h-4 text-emerald-400" />
            <span class="text-zinc-500 text-xs uppercase tracking-wider">Status</span>
            <span class="text-white font-medium">{{ node.pipeline_status }}</span>
        </p>
        <p v-if="node.status_message" class="text-xs text-zinc-400 pl-6">{{ node.status_message }}</p>
      </div>
    </div>
    <div class="mt-5 space-y-2">
      <div v-if="isReadOnly" class="w-full flex items-center justify-center space-x-2 border border-dashed border-white/10 text-zinc-500 font-medium py-2 px-4 rounded-xl">
        <EyeOff class="w-4 h-4" />
        <span>Read-only view</span>
      </div>
      <template v-else>
        <button v-if="canEditSub" @click="showSubModal = true" class="w-full flex items-center justify-center space-x-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 font-semibold py-2 px-4 rounded-xl transition-colors">
          <Link class="w-4 h-4" />
          <span>Manage Subscription URL</span>
        </button>
        <button v-if="canSwitchVpn" @click="switchVpnProfile" class="w-full flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 rounded-xl transition-colors">
          <Shield class="w-4 h-4" />
          <span>Switch VPN Configuration</span>
        </button>
        <button v-if="canEditSub" @click="confirmDeleteNode" class="w-full flex items-center justify-center space-x-2 bg-red-500/10 hover:bg-red-500/20 border border-red-500/20 text-red-300 font-semibold py-2 px-4 rounded-xl transition-colors">
          <Trash2 class="w-4 h-4" />
          <span>Delete Node</span>
        </button>
        <button v-if="canEditSub && !isTerminated" @click="confirmTerminate" class="w-full flex items-center justify-center space-x-2 bg-red-500/10 hover:bg-red-500/20 border border-red-500/40 text-red-300 font-semibold py-2 px-4 rounded-xl transition-colors">
          <Power class="w-4 h-4" />
          <span>Terminate &amp; Self-Destruct</span>
        </button>
      </template>
    </div>

    <!-- Rename Modal -->
    <div v-if="showRenameModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Rename Node</h2>
        <div class="space-y-4">
          <div>
            <label for="new_node_name" class="block text-sm font-medium text-zinc-400">New Name</label>
            <input v-model="newNodeName" type="text" id="new_node_name" placeholder="My Device" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
          </div>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showRenameModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="renameNode" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Rename</button>
        </div>
      </div>
    </div>

    <!-- Terminate Confirm Modal -->
    <div v-if="showTerminateModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-red-500/40 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-2 tracking-tight text-red-300">Terminate Node</h2>
        <p class="text-zinc-400 mb-5">This will make the node <strong class="text-red-300">self-destruct</strong>: it tears down its VPN containers, wipes its local config and goes offline permanently. It cannot be undone.</p>
        <p class="text-sm text-zinc-500 mb-2">Type <span class="font-mono text-red-300 font-bold">TERMINATE</span> to confirm:</p>
        <input v-model="terminateConfirm" type="text" placeholder="TERMINATE" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-red-500 focus:border-red-500/50">
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showTerminateModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="terminateNode" :disabled="terminateConfirm !== 'TERMINATE'" class="px-4 py-2 bg-red-500/20 hover:bg-red-500/30 border border-red-500/40 text-red-200 rounded-xl transition-colors disabled:opacity-40 disabled:cursor-not-allowed">
            Terminate
          </button>
        </div>
      </div>
    </div>

    <!-- Subscription URL Modal -->
    <div v-if="showSubModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Manage Subscription URL</h2>
        <div class="space-y-4">
          <div>
            <label class="block text-sm font-medium text-zinc-400">Current Subscription URL</label>
            <p class="mt-1 text-sm text-zinc-300 break-all">{{ node.sub_url || 'Not configured' }}</p>
          </div>
          <div>
            <label for="sub_url" class="block text-sm font-medium text-zinc-400">New Subscription URL</label>
            <input v-model="newSubUrl" type="text" id="sub_url" placeholder="https://example.com/subscription" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
          </div>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showSubModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="updateSubUrl" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Update URL</button>
        </div>
      </div>
    </div>

    <!-- Switch VPN Modal -->
    <div v-if="showSwitchModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
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
import { ref, computed, inject } from 'vue';
import { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Trash2, Pencil, Power, Copy, EyeOff } from 'lucide-vue-next';

const ONLINE_THRESHOLD_SECONDS = 90;

export default {
  name: 'NodeCard',
  components: { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Trash2, Pencil, Power, Copy, EyeOff },
  props: {
    node: {
      type: Object,
      required: true,
    },
  },
  emits: ['node-updated', 'node-deleted'],
  setup(props, { emit }) {
    const authCtx = inject('authCtx', {});
    const canEditSub = computed(() => authCtx.canEditSub?.value ?? false);
    const canSwitchVpn = computed(() => authCtx.canSwitchVpn?.value ?? false);
    const isReadOnly = computed(() => authCtx.isReadOnly?.value ?? false);

    const showSubModal = ref(false);
    const newSubUrl = ref('');
    const showRenameModal = ref(false);
    const newNodeName = ref('');
    const showTerminateModal = ref(false);
    const terminateConfirm = ref('');

    const isOnline = computed(() => {
      if (!props.node.last_seen) return false;
      const lastSeen = new Date(props.node.last_seen);
      const diffSeconds = (new Date() - lastSeen) / 1000;
      return diffSeconds < ONLINE_THRESHOLD_SECONDS;
    });

    const isTerminated = computed(() => (props.node.pipeline_status || '').toLowerCase() === 'terminated');

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

    const copySubUrl = async () => {
      if (!props.node.sub_url) return;
      try {
        await navigator.clipboard.writeText(props.node.sub_url);
        alert('Subscription URL copied to clipboard.');
      } catch (error) {
        console.error('Copy failed:', error);
        alert('Could not copy to clipboard.');
      }
    };

    const confirmTerminate = () => {
      if (confirm(`Terminate node "${props.node.name}"?\n\nThe node will self-destruct: engines torn down, local config wiped, agent exits permanently.`)) {
        showTerminateModal.value = true;
      }
    };

    const terminateNode = async () => {
      if (terminateConfirm.value !== 'TERMINATE') return;
      try {
        const response = await fetch(`/api/web/nodes/${props.node.id}/terminate`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
        });
        if (!response.ok) throw new Error('Failed to queue terminate');
        showTerminateModal.value = false;
        terminateConfirm.value = '';
        alert('Terminate queued. The node will self-destruct on its next poll.');
        emit('node-updated');
      } catch (error) {
        console.error('Error terminating node:', error);
        alert('Could not queue terminate.');
      }
    };

    const renameNode = async () => {
      const name = newNodeName.value.trim();
      if (!name) {
        alert('Please enter a name.');
        return;
      }
      try {
        const response = await fetch(`/api/web/nodes/${props.node.id}/rename`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name }),
        });
        if (!response.ok) throw new Error('Failed to rename node');
        showRenameModal.value = false;
        newNodeName.value = '';
        alert('Node renamed!');
        emit('node-updated');
      } catch (error) {
        console.error('Error renaming node:', error);
        alert('Could not rename node.');
      }
    };

    return {
      isOnline,
      isTerminated,
      nodeIcon,
      pipelineStatusIcon,
      timeSince,
      showSubModal,
      newSubUrl,
      updateSubUrl,
      copySubUrl,
      confirmDeleteNode,
      deleteNode,
      switchVpnProfile,
      showSwitchModal,
      switchTo,
      showRenameModal,
      newNodeName,
      renameNode,
      showTerminateModal,
      terminateConfirm,
      confirmTerminate,
      terminateNode,
    };
  },
};
</script>
