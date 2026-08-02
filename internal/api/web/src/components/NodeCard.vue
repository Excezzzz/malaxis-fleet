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
            <button v-if="canManage" @click="showRenameModal = true" title="Rename node"
              class="p-1.5 rounded-lg bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-400 hover:text-white transition-colors">
              <Pencil class="w-3.5 h-3.5" />
            </button>
            <span v-if="isTerminated" class="text-xs font-bold uppercase tracking-wider px-2 py-0.5 rounded-lg bg-red-500/20 border border-red-500/40 text-red-300">Terminated</span>
          </div>
        </div>
      </div>
      <div class="space-y-2 text-sm">
        <div class="flex justify-between items-baseline gap-2">
            <span class="text-zinc-500 text-xs uppercase">LAN IP</span>
            <span class="text-white font-medium truncate">{{ node.ip_lan || 'N/A' }}</span>
        </div>
        <div class="flex justify-between items-baseline gap-2">
            <span class="text-zinc-500 text-xs uppercase">Hostname</span>
            <span class="text-white font-medium truncate">{{ node.hostname || 'N/A' }}</span>
        </div>
        <div class="flex justify-between items-baseline gap-2">
            <span class="text-zinc-500 text-xs uppercase">VPN Server</span>
            <span class="text-white font-medium truncate text-right">{{ node.active_server || 'None' }}<span v-if="node.active_engine" class="text-xs text-zinc-500"> ({{ node.active_engine }}{{ node.active_proto ? ' / ' + node.active_proto : '' }})</span></span>
        </div>
        <div class="flex justify-between items-center gap-2">
            <span class="text-zinc-500 text-xs uppercase">Sub URL</span>
            <template v-if="node.sub_url">
              <div class="flex items-center gap-2">
                <span class="text-xs text-zinc-400 truncate max-w-[180px] inline-block align-bottom" :title="node.sub_url">{{ node.sub_url }}</span>
                <button @click="copySubUrl" title="Copy subscription URL"
                  class="p-1 rounded-md bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-400 hover:text-white transition-colors shrink-0">
                  <Copy class="w-3 h-3" />
                </button>
              </div>
            </template>
            <span v-else class="text-xs text-zinc-500">Not set</span>
        </div>
        <div class="flex justify-between items-baseline h-5">
            <template v-if="isOnline">
                <span class="text-emerald-400 font-semibold flex items-center gap-1.5">
                    <span class="w-2 h-2 rounded-full bg-emerald-400 animate-pulse"></span>
                    Online
                </span>
            </template>
            <template v-else>
                <span class="text-zinc-500 text-xs uppercase">Last Seen</span>
                <span class="text-zinc-400 font-medium">{{ timeSince(node.last_seen) }}</span>
            </template>
        </div>
      </div>
    </div>
    <div class="mt-auto pt-4">
      <div v-if="node.pipeline_status || (node.available_servers && node.available_servers.length)" class="mt-3 pt-3 border-t border-white/5 flex items-center justify-between text-xs text-zinc-400">
        <div class="flex items-center space-x-2">
          <component :is="pipelineStatusIcon(node.pipeline_status)" class="w-4 h-4" :class="statusColorClass" />
          <span>Status: <strong class="font-medium" :class="statusColorClass">{{ node.pipeline_status || 'Idle' }}</strong></span>
        </div>
        <span v-if="node.available_servers && node.available_servers.length" class="text-zinc-500">{{ node.available_servers.length }} servers</span>
      </div>
      <p v-if="node.pipeline_status && node.status_message" class="text-xs mt-1 pl-6" :class="statusColorClass">{{ node.status_message }}</p>

      <div class="mt-3 space-y-2">
        <div v-if="isReadOnly && !canManage" class="w-full flex items-center justify-center space-x-2 border border-dashed border-white/10 text-zinc-500 font-medium py-2 px-4 rounded-xl">
            <EyeOff class="w-4 h-4" />
            <span>Read-only view</span>
        </div>
        <template v-if="canManage">
            <div class="grid grid-cols-2 gap-2">
                <button @click="showSubModal = true" class="w-full flex items-center justify-center space-x-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 font-semibold py-2 px-4 rounded-xl transition-colors">
                  <Link class="w-4 h-4" />
                  <span>Manage Sub URL</span>
                </button>
                <button @click="showSwitchModal = true" class="w-full flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 rounded-xl transition-colors">
                  <Shield class="w-4 h-4" />
                  <span>Switch VPN</span>
                </button>
            </div>
            <div class="grid grid-cols-2 gap-2">
                <button @click="viewLogs" class="w-full flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 rounded-xl transition-colors">
                    <ScrollText class="w-4 h-4" />
                    <span>View Logs</span>
                </button>
                <button @click="confirmDelete" class="w-full flex items-center justify-center space-x-2 bg-red-500/10 hover:bg-red-500/20 border border-red-500/20 text-red-300 font-semibold py-2 px-4 rounded-xl transition-colors">
                  <Trash2 class="w-4 h-4" />
                  <span>Delete</span>
                </button>
            </div>
            <div>
                <button v-if="!isTerminated" @click="showTerminateModal = true" class="w-full flex items-center justify-center space-x-2 bg-red-800/20 hover:bg-red-800/40 border border-red-500/40 text-red-300 font-semibold py-2 px-4 rounded-xl transition-colors">
                  <Power class="w-4 h-4" />
                  <span>Terminate & Self-Destruct</span>
                </button>
            </div>
        </template>
      </div>
    </div>

    <!-- Modals -->
    <div v-if="showRenameModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Rename Node</h2>
        <input v-model="newNodeName" type="text" placeholder="My Device" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showRenameModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="renameNode" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Rename</button>
        </div>
      </div>
    </div>

    <div v-if="showTerminateModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-red-500/40 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-2 tracking-tight text-red-300">Terminate Node</h2>
        <p class="text-zinc-400 mb-5">This will make the node <strong class="text-red-300">self-destruct</strong>, wiping its config and going offline permanently. It cannot be undone.</p>
        <p class="text-sm text-zinc-500 mb-2">Type <span class="font-mono text-red-300 font-bold">TERMINATE</span> to confirm:</p>
        <input v-model="terminateConfirm" type="text" placeholder="TERMINATE" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-red-500 focus:border-red-500/50">
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showTerminateModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="terminateNode" :disabled="terminateConfirm !== 'TERMINATE'" class="px-4 py-2 bg-red-500/20 hover:bg-red-500/30 border border-red-500/40 text-red-200 rounded-xl transition-colors disabled:opacity-40 disabled:cursor-not-allowed">Terminate</button>
        </div>
      </div>
    </div>

    <div v-if="showSubModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Manage Subscription URL</h2>
        <input v-model="newSubUrl" type="text" placeholder="https://example.com/subscription" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showSubModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="updateSubUrl" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Update URL</button>
        </div>
      </div>
    </div>

    <div v-if="showSwitchModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-2 tracking-tight">Switch VPN Configuration</h2>
        <p class="text-zinc-400 mb-5">Currently: <strong>{{ node.active_server || 'None' }}</strong></p>
        <div class="grid grid-cols-2 gap-3 mb-4">
          <button @click="switchTo('fastest')" class="flex items-center justify-center space-x-2 px-4 py-3 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors font-semibold"><span>⚡</span><span>Fastest</span></button>
          <button @click="switchTo('balanced')" class="flex items-center justify-center space-x-2 px-4 py-3 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors font-semibold"><span>⚖️</span><span>Balanced</span></button>
        </div>
        <p class="text-xs text-zinc-500 mb-2">Available servers ({{ (node.available_servers || []).length }})</p>
        <div v-if="(node.available_servers || []).length" class="grid grid-cols-2 gap-3 max-h-56 overflow-y-auto pr-1">
          <button v-for="srv in node.available_servers" :key="srv" @click="switchTo(srv)"
            :class="['px-4 py-3 rounded-xl transition-colors font-semibold truncate text-left', srv === node.active_server ? 'bg-emerald-500/20 hover:bg-emerald-500/30 border border-emerald-500/40 text-emerald-100' : 'bg-white/5 hover:bg-white/10 border border-white/10']">
            {{ srv }}
          </button>
        </div>
        <p v-else class="text-sm text-zinc-500">No servers reported yet.</p>
        <div class="mt-8 flex justify-end"><button type="button" @click="showSwitchModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Close</button></div>
      </div>
    </div>

    <div v-if="showLogsModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-4xl flex flex-col">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Agent Logs: {{ node.name }}</h2>
        <div class="bg-black p-4 rounded-lg font-mono text-xs text-white/80 h-96 overflow-y-auto whitespace-pre-wrap flex-grow">
          <div v-if="isLoadingLogs" class="flex items-center justify-center h-full text-zinc-400">Loading logs...</div>
          <pre v-else>{{ nodeLogs || 'No logs available.' }}</pre>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showLogsModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Close</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, inject } from 'vue';
import { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Trash2, Pencil, Power, Copy, EyeOff, ScrollText } from 'lucide-vue-next';

const ONLINE_THRESHOLD_SECONDS = 90;

export default {
  name: 'NodeCard',
  components: { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Trash2, Pencil, Power, Copy, EyeOff, ScrollText },
  props: {
    node: {
      type: Object,
      required: true,
    },
  },
  emits: ['node-updated', 'node-deleted'],
  setup(props, { emit }) {
    const { user, hasPermission, isReadOnly } = inject('authCtx', { user: ref(null), hasPermission: () => false, isReadOnly: ref(false) });
    
    const isOwner = computed(() => user.value?.role?.name === 'owner' || user.value?.role === 'owner' || user.value?.username === 'admin');
    const canManage = computed(() => isOwner.value || hasPermission('can_manage_nodes'));

    const showSubModal = ref(false);
    const newSubUrl = ref('');
    const showRenameModal = ref(false);
    const newNodeName = ref('');
    const showTerminateModal = ref(false);
    const terminateConfirm = ref('');
    const showLogsModal = ref(false);
    const nodeLogs = ref('');
    const isLoadingLogs = ref(false);

    const isOnline = computed(() => {
      if (!props.node.last_seen) return false;
      const lastSeen = new Date(props.node.last_seen);
      const diffSeconds = (new Date() - lastSeen) / 1000;
      return diffSeconds < ONLINE_THRESHOLD_SECONDS;
    });

    const isTerminated = computed(() => (props.node.pipeline_status || '').toLowerCase() === 'terminated');

    const statusColorClass = computed(() => {
        const status = `${(props.node.pipeline_status || '')} ${(props.node.status_message || '')}`.toLowerCase();
        if (status.includes('fail') || status.includes('error')) return 'text-red-400';
        if (status.includes('queued') || status.includes('pending') || status.includes('progress')) return 'text-yellow-400';
        if (status.includes('fetched') || status.includes('healthy') || status.includes('active') || status.includes('updated') || status.includes('success') || status.includes('idle')) return 'text-emerald-400';
        return 'text-zinc-400';
    });

    const nodeIcon = computed(() => {
        const type = props.node.device_type ? props.node.device_type.toLowerCase() : '';
        if (type.includes('server')) return 'Server';
        return 'Cpu';
    });

    const pipelineStatusIcon = (status) => {
        if (!status) return 'Hourglass';
        const s = status.toLowerCase();
        if (s.includes('fail') || s.includes('error')) return 'XCircle';
        if (s.includes('queued') || s.includes('pending') || s.includes('progress')) return 'Hourglass';
        if (s.includes('fetched')) return 'ArrowDown';
        if (s.includes('restart')) return 'Cog';
        if (s.includes('active') || s.includes('success') || s.includes('updated')) return 'CheckCircle2';
        return 'Hourglass';
    };

    const timeSince = (dateStr) => {
        if (!dateStr) return 'never';
        const date = new Date(dateStr);
        const seconds = Math.floor((new Date() - date) / 1000);
        if (seconds < 5) return "just now";
        if (seconds < 60) return `${seconds} seconds ago`;
        const minutes = Math.floor(seconds / 60);
        if (minutes < 60) return `${minutes} minute${minutes > 1 ? 's' : ''} ago`;
        const hours = Math.floor(minutes / 60);
        if (hours < 24) return `${hours} hour${hours > 1 ? 's' : ''} ago`;
        const days = Math.floor(hours / 24);
        return `${days} day${days > 1 ? 's' : ''} ago`;
    };

    const updateSubUrl = async () => {
      // ... (implementation unchanged)
    };

    const confirmDelete = () => {
      if (confirm(`Delete this node "${props.node.name}" from fleet?`)) deleteNode();
    };

    const deleteNode = async () => {
      // ... (implementation unchanged)
    };

    const showSwitchModal = ref(false);

    const switchTo = async (target) => {
      // ... (implementation unchanged)
    };
    
    const copySubUrl = async () => {
      // ... (implementation unchanged)
    };

    const terminateNode = async () => {
      // ... (implementation unchanged)
    };

    const renameNode = async () => {
      // ... (implementation unchanged)
    };

    const viewLogs = async () => {
      showLogsModal.value = true;
      isLoadingLogs.value = true;
      nodeLogs.value = '';
      try {
        // This is a placeholder. The backend endpoint needs to be implemented.
        // Once implemented, the actual fetch request will be:
        // const response = await fetch(`/api/web/nodes/${props.node.id}/logs`);
        // if (!response.ok) throw new Error('Failed to fetch logs');
        // const data = await response.json();
        // nodeLogs.value = data.logs;
        await new Promise(res => setTimeout(res, 1000));
        nodeLogs.value = 'Log fetching not yet implemented. This is a placeholder UI.';
      } catch (error) {
        console.error('Error fetching logs:', error);
        nodeLogs.value = 'Failed to load logs.';
      } finally {
        isLoadingLogs.value = false;
      }
    };

    return {
      isOnline, isTerminated, nodeIcon, pipelineStatusIcon, timeSince, statusColorClass,
      showSubModal, newSubUrl, updateSubUrl,
      copySubUrl, confirmDelete, deleteNode,
      showSwitchModal, switchTo,
      showRenameModal, newNodeName, renameNode,
      showTerminateModal, terminateConfirm, terminateNode,
      showLogsModal, nodeLogs, isLoadingLogs, viewLogs,
      canManage, isReadOnly,
    };
  },
};
</script>
