<template>
  <div :class="['relative bg-zinc-900 border rounded-2xl p-5 flex flex-col justify-between h-full transition-colors duration-300 hover:border-indigo-500/30 hover:bg-zinc-800/80', isTerminated ? 'border-red-500/50 bg-red-950/20' : 'border-white/10']">
    <span v-if="isOnline" class="absolute top-4 right-4 w-3 h-3 rounded-full bg-emerald-500 shadow-md shadow-emerald-500/50 animate-pulse" title="Online"></span>
    <span v-else class="absolute top-4 right-4 w-3 h-3 rounded-full bg-red-500/80" title="Offline"></span>
    <div>
      <div class="flex justify-between items-start mb-4 pr-8">
        <div class="flex items-center space-x-3 min-w-0">
          <div class="p-2 rounded-xl bg-white/5 border border-white/10 shrink-0">
            <component :is="nodeIcon" class="w-6 h-6 text-indigo-300" />
          </div>
          <div class="flex items-center space-x-2 min-w-0">
            <h2 class="text-xl font-bold tracking-tight truncate">{{ node.name }}</h2>
            <button v-if="canRename" @click="showRenameModal = true" title="Rename node"
              class="p-2 rounded-lg bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-400 hover:text-white transition-colors">
              <Pencil class="w-3.5 h-3.5" />
            </button>
            <button v-if="canDelete" @click="confirmDelete()" title="Delete node"
              class="p-2 rounded-lg hover:bg-red-500/10 transition-colors">
              <svg class="w-4 h-4 text-zinc-500 hover:text-red-400 cursor-pointer transition-colors" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
              </svg>
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
                <span class="text-xs text-zinc-400 truncate max-w-[140px] sm:max-w-[180px] inline-block align-bottom" :title="node.sub_url">{{ node.sub_url }}</span>
                <button @click="copySubUrl" title="Copy subscription URL"
                  class="p-1 rounded-md bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-400 hover:text-white transition-colors shrink-0">
                  <Copy class="w-3 h-3" />
                </button>
              </div>
            </template>
            <span v-else class="text-xs text-zinc-500">Not set</span>
        </div>
        <div class="flex justify-between items-baseline gap-2">
            <span class="text-zinc-500 text-xs uppercase">Last Seen</span>
            <span class="text-white font-medium truncate">{{ isOnline ? 'just now' : timeSince(node.last_seen) }}</span>
        </div>
      </div>
    </div>
    <div class="mt-auto pt-4">
      <button v-if="node.pipeline_status || (node.available_servers && node.available_servers.length)" type="button" @click="activeModal = 'status'"
        class="mt-3 w-full h-10 flex items-center gap-2 px-3 rounded-xl bg-zinc-900/40 border border-white/10 hover:border-white/20 hover:bg-zinc-800/60 transition-colors cursor-pointer text-left group">
        <span class="shrink-0 flex items-center justify-center w-6 h-6 rounded-lg" :class="statusBgClass">
          <component :is="pipelineStatusIcon(node.pipeline_status)" class="w-3.5 h-3.5" :class="statusColorClass" />
        </span>
        <span class="flex-1 min-w-0 truncate whitespace-nowrap leading-none">
          <strong class="font-semibold" :class="statusColorClass">{{ node.pipeline_status || 'Idle' }}</strong>
          <template v-if="node.status_message && !isTaskQueued">
            <span class="text-zinc-600 mx-1">·</span>
            <span :class="statusColorClass">{{ node.status_message }}</span>
          </template>
        </span>
        <span v-if="(node.available_servers || []).length" class="shrink-0 px-2 py-0.5 rounded-md text-[10px] font-semibold uppercase tracking-wide bg-indigo-500/15 border border-indigo-500/30 text-indigo-300">
          {{ node.available_servers.length }} configs
        </span>
        <ChevronRight class="w-4 h-4 shrink-0 text-zinc-600 group-hover:text-zinc-200 group-hover:translate-x-0.5 transition-all" />
      </button>

      <div class="mt-3 space-y-2">
        <div v-if="isReadOnly && !canManage" class="w-full flex items-center justify-center space-x-2 border border-dashed border-white/10 text-zinc-500 font-medium py-2 px-4 rounded-xl">
            <EyeOff class="w-4 h-4" />
            <span>Read-only view</span>
        </div>
        <template v-if="canManage">
            <div class="flex flex-wrap gap-2">
                <button v-if="canEditSubCard" @click="showSubModal = true" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                  <Link class="w-4 h-4" />
                  <span class="font-mono text-sm">[Manage Sub URL]</span>
                </button>
                <button v-if="canSwitch && !isTerminated" @click="showSwitchModal = true" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                  <Shield class="w-4 h-4" />
                  <span class="font-mono text-sm">[Switch VPN]</span>
                </button>
            </div>
            <div class="flex flex-wrap gap-2">
                <button v-if="canViewNodeLogs" @click="openLogs" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                    <ScrollText class="w-4 h-4" />
                    <span class="font-mono text-sm">[View Logs]</span>
                </button>
                <button v-if="canSwitch" @click="showTaskQueueModal = true" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                  <Hourglass class="w-4 h-4" />
                  <span class="font-mono text-sm">[Task Queue ({{ pendingCommandCount }})]</span>
                </button>
                <button v-if="canUpdateClient" @click="pushClientFiles" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                  <RefreshCw class="w-4 h-4" />
                  <span class="font-mono text-sm">[Push Client Files]</span>
                </button>
            </div>
            </template>
      </div>

      <div v-if="toast" :class="['fixed bottom-6 right-6 z-50 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl border', toastType === 'success' ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-200' : 'bg-red-500/15 border-red-500/40 text-red-200']">
        {{ toast }}
      </div>
    </div>

    <!-- Modals -->
    <div v-if="showRenameModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="showRenameModal = false">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-6 sm:p-8 w-full max-w-md max-h-[90vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>Rename Node<span class="font-mono text-indigo-400">]</span></h2>
        <input v-model="newNodeName" type="text" placeholder="My Device" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showRenameModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="renameNode" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Rename</button>
        </div>
      </div>
    </div>

    <div v-if="showDeleteModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="showDeleteModal = false">
      <div class="bg-zinc-900 border border-red-500/40 rounded-2xl shadow-2xl p-6 sm:p-8 w-full max-w-md max-h-[90vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-2 tracking-tight text-red-300"><span class="font-mono text-red-400">[</span>Delete Node<span class="font-mono text-red-400">]</span></h2>
        <p class="text-zinc-400 mb-6 text-sm">Choose how to remove <strong class="text-white">{{ node.name }}</strong> from the fleet.</p>
        <button v-if="canSoftDelete" @click="softDeleteNode" class="w-full flex items-center justify-between px-4 py-3 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors text-left mb-3">
          <span>
            <span class="block font-semibold text-white">Soft Delete</span>
            <span class="block text-xs text-zinc-400 mt-1">Remove from dashboard. The client device keeps running and will re-register on its next poll.</span>
          </span>
        </button>
        <div v-if="canTerminate" class="rounded-xl border border-red-500/30 bg-red-900/10 p-4">
          <button @click="deleteChoice = 'terminate'" class="w-full flex items-center justify-between text-left">
            <span>
              <span class="block font-semibold text-red-300">Terminate & Self-Destruct</span>
              <span class="block text-xs text-red-200/60 mt-1">Wipe the client: tears down engine containers, wipes local config and goes offline permanently.</span>
            </span>
          </button>
          <template v-if="deleteChoice === 'terminate'">
            <p class="text-xs text-zinc-500 mt-3 mb-1">Type <span class="font-mono text-red-300 font-bold">TERMINATE</span> to confirm:</p>
            <input v-model="terminateConfirm" type="text" placeholder="TERMINATE" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-red-500 focus:border-red-500/50">
          </template>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showDeleteModal = false; deleteChoice = ''; terminateConfirm = ''" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button v-if="deleteChoice === 'terminate' && canTerminate" type="button" @click="terminateNode" :disabled="terminateConfirm !== 'TERMINATE'" class="px-4 py-2 bg-red-500/20 hover:bg-red-500/30 border border-red-500/40 text-red-200 rounded-xl transition-colors disabled:opacity-40 disabled:cursor-not-allowed">Terminate</button>
        </div>
      </div>
    </div>

    <div v-if="showTaskQueueModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="showTaskQueueModal = false">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-6 sm:p-8 w-full max-w-md max-h-[90vh] overflow-y-auto">
        <div class="flex items-center justify-between mb-4">
          <h2 class="text-2xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>Task Queue<span class="font-mono text-indigo-400">]</span></h2>
          <button type="button" @click="showTaskQueueModal = false" class="px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg transition-colors">Close</button>
        </div>
        <div v-if="node.pending_command" class="flex items-center justify-between bg-amber-500/10 border border-amber-500/30 rounded-lg px-3 py-2">
          <div class="min-w-0">
            <p class="text-xs text-amber-200 font-medium flex items-center space-x-2">
              <Hourglass class="w-3.5 h-3.5 shrink-0" />
              <span>Pending Task: {{ pendingTaskLabel }}</span>
            </p>
            <code class="block mt-1 text-xs text-zinc-400 break-all whitespace-pre-wrap">{{ node.pending_command }}</code>
          </div>
        </div>
        <div v-else class="text-sm text-zinc-500 py-6 text-center">No pending tasks. The queue is empty.</div>
        <div class="mt-6 flex justify-end space-x-4">
          <button v-if="node.pending_command" type="button" @click="cancelPendingCommand" class="px-4 py-2 bg-red-500/15 hover:bg-red-500/30 border border-red-500/30 text-red-200 rounded-xl transition-colors flex items-center space-x-2">
            <X class="w-4 h-4" />
            <span>Cancel Pending Task</span>
          </button>
        </div>
      </div>
    </div>

    <div v-if="showSubModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="showSubModal = false">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-6 sm:p-8 w-full max-w-md max-h-[90vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>Manage Subscription URL<span class="font-mono text-indigo-400">]</span></h2>
        <input v-model="newSubUrl" type="text" placeholder="https://example.com/subscription" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showSubModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
          <button type="button" @click="updateSubUrl" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Update URL</button>
        </div>
      </div>
    </div>

    <div v-if="showSwitchModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="showSwitchModal = false">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-6 sm:p-8 w-full max-w-md max-h-[90vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-2 tracking-tight"><span class="font-mono text-indigo-400">[</span>Switch VPN Configuration<span class="font-mono text-indigo-400">]</span></h2>
        <p class="text-zinc-400 mb-5">Currently: <strong>{{ node.active_server || 'None' }}</strong></p>
        <div class="grid grid-cols-2 gap-3 mb-4">
          <button @click="switchTo('fastest')" class="flex items-center justify-center space-x-2 px-4 py-3 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors font-semibold text-zinc-200"><Zap class="w-4 h-4 text-indigo-400" /><span>Fastest</span></button>
          <button @click="switchTo('balanced')" class="flex items-center justify-center space-x-2 px-4 py-3 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors font-semibold text-zinc-200"><Scale class="w-4 h-4 text-indigo-400" /><span>Balanced</span></button>
        </div>
        <p class="text-xs text-zinc-500 mb-2">Available configs ({{ (node.available_servers || []).length }})</p>
        <div v-if="(node.available_servers || []).length" class="grid grid-cols-2 gap-3 max-h-56 overflow-y-auto pr-1">
          <button v-for="srv in node.available_servers" :key="srv" @click="switchTo(srv)"
            :class="['px-4 py-3 rounded-xl transition-colors font-semibold truncate text-left', srv === node.active_server ? 'bg-emerald-500/20 hover:bg-emerald-500/30 border border-emerald-500/40 text-emerald-100' : 'bg-white/5 hover:bg-white/10 border border-white/10']">
            {{ srv }}
          </button>
        </div>
        <p v-else class="text-sm text-zinc-500">No configs reported yet.</p>
        <div class="mt-8 flex justify-end"><button type="button" @click="showSwitchModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Close</button></div>
      </div>
    </div>

    <div v-if="showLogsModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="closeLogs">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-6 w-full max-w-5xl flex flex-col max-h-[90vh]">
        <div class="flex items-center justify-between mb-4">
          <h2 class="text-2xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>Agent Logs: {{ node.name }}<span class="font-mono text-indigo-400">]</span></h2>
          <button type="button" @click="closeLogs" class="px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg transition-colors">Close</button>
        </div>
        <div class="flex flex-wrap items-center gap-3 mb-3">
          <div class="flex rounded-lg bg-black/40 border border-white/10 overflow-hidden">
            <button v-for="c in logContainers" :key="c" @click="selectContainer(c)"
              :class="['px-4 py-2 text-sm font-medium transition-colors', logContainer === c ? 'bg-indigo-500/25 text-white' : 'text-zinc-400 hover:text-white hover:bg-white/5']">
              {{ c }}
            </button>
          </div>
          <div class="flex items-center gap-2 ml-auto">
            <label class="flex items-center gap-2 text-xs text-zinc-400 cursor-pointer select-none">
              <input type="checkbox" v-model="autoRefreshLogs" class="accent-indigo-500" />
              Auto-refresh
            </label>
            <select v-model="logRefreshInterval" :disabled="!autoRefreshLogs"
              class="bg-black/40 border border-white/10 rounded-lg px-2 py-1 text-xs text-zinc-300 focus:outline-none disabled:opacity-40">
              <option :value="3000">3s</option>
              <option :value="5000">5s</option>
              <option :value="10000">10s</option>
              <option :value="30000">30s</option>
            </select>
            <button @click="fetchLogs" title="Refresh"
              class="flex items-center gap-2 px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg text-sm text-white transition-colors">
              <RefreshCw :class="['w-4 h-4', isLoadingLogs ? 'animate-spin' : '']" />
              <span>Refresh</span>
            </button>
            <button @click="copyLogs" title="Copy logs"
              class="flex items-center gap-2 px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg text-sm text-white transition-colors">
              <Copy class="w-4 h-4" />
              <span>Copy</span>
            </button>
          </div>
        </div>
        <div class="bg-black p-4 rounded-lg font-mono text-xs text-white/80 h-64 sm:h-96 min-h-0 overflow-y-auto whitespace-pre-wrap flex-1" ref="logHost">
          <div v-if="isLoadingLogs && !nodeLogs" class="flex items-center justify-center h-full text-zinc-400">Loading logs...</div>
          <pre v-else>{{ nodeLogs || 'No logs available yet. Press Refresh or wait for auto-refresh.' }}</pre>
        </div>
      </div>
    </div>

    <div v-if="activeModal === 'status'" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="activeModal = ''">
      <div class="bg-zinc-900 border border-white/10 rounded-2xl shadow-2xl p-6 sm:p-8 w-full max-w-md max-h-[90vh] overflow-y-auto">
        <div class="flex items-center justify-between mb-6">
          <h2 class="text-2xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>Detailed Status<span class="font-mono text-indigo-400">]</span></h2>
          <button type="button" @click="activeModal = ''" class="px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg transition-colors">Close</button>
        </div>
        <div class="space-y-5 text-sm">
          <div>
            <p class="text-xs uppercase tracking-wider text-zinc-500 mb-1">Pipeline Status</p>
            <p class="text-base font-semibold" :class="statusColorClass">{{ node.pipeline_status || 'Idle' }}</p>
          </div>
          <div>
            <p class="text-xs uppercase tracking-wider text-zinc-500 mb-1">Message</p>
            <p class="text-zinc-300 leading-relaxed break-words whitespace-pre-wrap">{{ node.status_message || 'No message.' }}</p>
          </div>
          <div>
            <p class="text-xs uppercase tracking-wider text-zinc-500 mb-1">Pending Command</p>
            <code class="block text-xs text-zinc-400 break-all whitespace-pre-wrap">{{ node.pending_command ? node.pending_command : 'None' }}</code>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, inject, watch, onUnmounted, nextTick } from 'vue';
import axios from 'axios';
import { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Pencil, Copy, EyeOff, ScrollText, X, ChevronRight, Zap, Scale } from 'lucide-vue-next';

const ONLINE_THRESHOLD_SECONDS = 90;

export default {
  name: 'NodeCard',
  components: { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Pencil, Copy, EyeOff, ScrollText, X, ChevronRight, Zap, Scale },
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
    const canRename = computed(() => isOwner.value || hasPermission('can_rename_node'));
    const canSoftDelete = computed(() => isOwner.value || hasPermission('can_edit_sub'));
    const canDelete = computed(() => isOwner.value || hasPermission('can_terminate_node') || hasPermission('can_edit_sub'));
    const canEditSubCard = computed(() => isOwner.value || hasPermission('can_edit_sub'));
    const canSwitch = computed(() => isOwner.value || hasPermission('can_switch_vpn'));
    const canViewNodeLogs = computed(() => isOwner.value || hasPermission('can_view_node_logs'));
    const canTerminate = computed(() => isOwner.value || hasPermission('can_terminate_node'));
    const canUpdateClient = computed(() => isOwner.value || hasPermission('can_update_client'));
    const canManage = computed(() => isOwner.value || hasPermission('can_edit_sub') || hasPermission('can_switch_vpn') || hasPermission('can_rename_node') || hasPermission('can_terminate_node') || hasPermission('can_purge_nodes') || hasPermission('can_update_client') || hasPermission('can_view_node_logs'));

    const showSubModal = ref(false);
    const newSubUrl = ref('');
    const showRenameModal = ref(false);
    const newNodeName = ref('');
    const showTerminateModal = ref(false);
    const terminateConfirm = ref('');
    const showDeleteModal = ref(false);
    const deleteChoice = ref('');
    const showTaskQueueModal = ref(false);
    const showLogsModal = ref(false);
    const activeModal = ref('');
    const nodeLogs = ref('');
    const isLoadingLogs = ref(false);
    const logContainers = ['node-agent', 'xray-node', 'singbox-node'];
    const logContainer = ref('node-agent');
    const autoRefreshLogs = ref(false);
    const logRefreshInterval = ref(3000);
    let logRefreshTimer = null;
    const logHost = ref(null);
    const toast = ref('');
    const toastType = ref('success');
    let toastTimer = null;

    const isOnline = computed(() => {
      if (!props.node.last_seen) return false;
      const lastSeen = new Date(props.node.last_seen);
      const diffSeconds = (new Date() - lastSeen) / 1000;
      return diffSeconds < ONLINE_THRESHOLD_SECONDS;
    });

    const isTerminated = computed(() => (props.node.pipeline_status || '').toLowerCase() === 'terminated');

    const isTaskQueued = computed(() => (props.node.pipeline_status || '').toLowerCase() === 'queued');

    const pendingTaskLabel = computed(() => {
        const cmd = props.node.pending_command || '';
        try {
            const parsed = JSON.parse(cmd);
            const action = parsed.command || parsed.action || (typeof parsed === 'string' ? parsed : '');
            return String(action).replace(/^switch[.:-]+/, 'switch to ').replace(/^update[.:-]+/, 'update ').replace(/^terminate.*/, 'terminate');
        } catch (e) {
            return cmd;
        }
    });

    const pendingCommandCount = computed(() => (props.node.pending_command ? 1 : 0));

    const statusColorClass = computed(() => {
        const status = `${(props.node.pipeline_status || '')} ${(props.node.status_message || '')}`.toLowerCase();
        if (status.includes('fail') || status.includes('error')) return 'text-red-400';
        if (status.includes('queued') || status.includes('pending') || status.includes('progress')) return 'text-yellow-400';
        if (status.includes('fetched') || status.includes('healthy') || status.includes('active') || status.includes('updated') || status.includes('success') || status.includes('idle')) return 'text-emerald-400';
        return 'text-zinc-400';
    });

    const statusBgClass = computed(() => {
        const status = `${(props.node.pipeline_status || '')} ${(props.node.status_message || '')}`.toLowerCase();
        if (status.includes('fail') || status.includes('error')) return 'bg-red-500/15 text-red-300';
        if (status.includes('queued') || status.includes('pending') || status.includes('progress')) return 'bg-yellow-500/15 text-yellow-300';
        if (status.includes('fetched') || status.includes('healthy') || status.includes('active') || status.includes('updated') || status.includes('success') || status.includes('idle')) return 'bg-emerald-500/15 text-emerald-300';
        return 'bg-white/10 text-zinc-300';
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

    const showToast = (msg, type = 'success', duration = 4000) => {
      toast.value = msg;
      toastType.value = type;
      clearTimeout(toastTimer);
      toastTimer = setTimeout(() => { toast.value = ''; }, duration);
    };

    const updateSubUrl = async () => {
      if (!newSubUrl.value) {
        showToast('Please enter a subscription URL.', 'error');
        return;
      }
      try {
        await axios.put(`/api/web/nodes/${props.node.id}/sub`, { sub_url: newSubUrl.value });
        showSubModal.value = false;
        emit('node-updated');
        showToast('Subscription URL updated.');
      } catch (e) {
        console.error('Error updating sub URL:', e);
        showToast('Failed to update subscription URL.', 'error');
      }
    };

    const pushClientFiles = async () => {
      if (!confirm(`Queue "update_client_files" for "${props.node.name}"?\n\nThe agent will download the latest templates and gracefully restart.`)) return;
      try {
        await axios.post('/api/web/nodes/update-client-files', { node_id: props.node.id });
        emit('node-updated');
        showToast('Client files update queued for this node.');
      } catch (e) {
        console.error('Error pushing client files:', e);
        showToast('Failed to queue client files update.', 'error');
      }
    };

    const confirmDelete = () => {
      deleteChoice.value = '';
      terminateConfirm.value = '';
      showDeleteModal.value = true;
    };

    const softDeleteNode = async () => {
      try {
        await axios.delete(`/api/web/devices/${props.node.id}`);
        showDeleteModal.value = false;
        deleteChoice.value = '';
        terminateConfirm.value = '';
        emit('node-deleted', props.node.id);
        showToast('Node deleted.');
      } catch (e) {
        console.error('Error deleting node:', e);
        showToast('Failed to delete node.', 'error');
      }
    };

    const showSwitchModal = ref(false);

    const switchTo = async (target) => {
      try {
        await axios.post(`/api/web/nodes/${props.node.id}/command`, { command: `switch:${target}` });
        showSwitchModal.value = false;
        emit('node-updated');
        showToast(`Switch queued to ${target}.`);
      } catch (e) {
        console.error('Error switching:', e);
        showToast('Failed to queue switch.', 'error');
      }
    };

    const copySubUrl = async () => {
      if (!props.node.sub_url) return;
      try {
        await navigator.clipboard.writeText(props.node.sub_url);
        showToast('Subscription URL copied.');
      } catch (e) {
        console.error('Failed to copy sub URL:', e);
      }
    };

    const terminateNode = async () => {
      if (terminateConfirm.value !== 'TERMINATE') return;
      try {
        await axios.post(`/api/web/nodes/${props.node.id}/terminate`);
        showDeleteModal.value = false;
        deleteChoice.value = '';
        terminateConfirm.value = '';
        emit('node-updated');
        showToast('Terminate queued. Node will self-destruct on next poll.');
      } catch (e) {
        console.error('Error queueing terminate:', e);
        showToast('Failed to queue terminate.', 'error');
      }
    };

    const cancelPendingCommand = async () => {
      try {
        await axios.put(`/api/web/nodes/${props.node.id}/clear-command`);
        props.node.pending_command = '';
        props.node.pending_msg_id = '';
        showTaskQueueModal.value = false;
        emit('node-updated');
        showToast('Pending task cancelled.');
      } catch (e) {
        console.error('Error clearing pending command:', e);
        showToast('Failed to cancel pending task.', 'error');
      }
    };

    const renameNode = async () => {
      if (!newNodeName.value) return;
      try {
        await axios.put(`/api/web/nodes/${props.node.id}/rename`, { name: newNodeName.value });
        showRenameModal.value = false;
        emit('node-updated');
        showToast('Node renamed.');
      } catch (e) {
        console.error('Error renaming node:', e);
        showToast('Failed to rename node.', 'error');
      }
    };

    const scrollLogsToBottom = async () => {
      await nextTick();
      if (logHost.value) logHost.value.scrollTop = logHost.value.scrollHeight;
    };

    const fetchLogs = async () => {
      isLoadingLogs.value = true;
      try {
        const response = await axios.get(`/api/web/nodes/${props.node.id}/logs`, {
          params: { container: logContainer.value },
        });
        nodeLogs.value = response.data.logs || '(no logs for ' + logContainer.value + ')';
        scrollLogsToBottom();
      } catch (e) {
        console.error('Error fetching logs:', e);
        nodeLogs.value = 'Failed to load logs.';
      } finally {
        isLoadingLogs.value = false;
      }
    };

    const selectContainer = (c) => {
      if (logContainer.value === c) return;
      logContainer.value = c;
      nodeLogs.value = '';
      stopAutoRefresh();
      fetchLogs();
    };

    const scheduleLogRefresh = () => {
      clearTimeout(logRefreshTimer);
      logRefreshTimer = setTimeout(async () => {
        await fetchLogs();
        scheduleLogRefresh();
      }, logRefreshInterval.value);
    };

    const stopAutoRefresh = () => {
      clearTimeout(logRefreshTimer);
      logRefreshTimer = null;
    };

    watch(logRefreshInterval, () => {
      if (autoRefreshLogs.value && showLogsModal.value) scheduleLogRefresh();
    });

    const openLogs = () => {
      showLogsModal.value = true;
      logContainer.value = 'node-agent';
      nodeLogs.value = '';
      fetchLogs();
      if (autoRefreshLogs.value) scheduleLogRefresh();
    };

    const closeLogs = () => {
      showLogsModal.value = false;
      stopAutoRefresh();
      autoRefreshLogs.value = false;
    };

    watch(autoRefreshLogs, (enabled) => {
      if (enabled && showLogsModal.value) {
        scheduleLogRefresh();
      } else {
        stopAutoRefresh();
      }
    });

    const copyLogs = async () => {
      if (!nodeLogs.value) return;
      try {
        await navigator.clipboard.writeText(nodeLogs.value);
        showToast('Logs copied.');
      } catch (e) {
        console.error('Failed to copy logs:', e);
      }
    };

    onUnmounted(() => {
      stopAutoRefresh();
    });

    return {
      isOnline, isTerminated, isTaskQueued, nodeIcon, pipelineStatusIcon, timeSince, statusColorClass, statusBgClass, pendingTaskLabel, pendingCommandCount,
      showSubModal, newSubUrl, updateSubUrl,
      pushClientFiles,
      copySubUrl, softDeleteNode, confirmDelete,
      showDeleteModal, deleteChoice,
      showTaskQueueModal,
      activeModal,
      showSwitchModal, switchTo,
      showRenameModal, newNodeName, renameNode,
      showTerminateModal, terminateConfirm, terminateNode,
      cancelPendingCommand,
      showLogsModal, nodeLogs, isLoadingLogs, openLogs, closeLogs, fetchLogs, selectContainer,
      logContainers, logContainer, autoRefreshLogs, logRefreshInterval, copyLogs,
      user, isOwner, canManage, canRename, canDelete, canEditSubCard, canSwitch, canViewNodeLogs,
      canTerminate, canUpdateClient, canSoftDelete,
      isReadOnly, toast, toastType, logHost,
    };
  },
};
</script>