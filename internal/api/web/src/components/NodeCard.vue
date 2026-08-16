<template>
  <div :class="['relative bg-zinc-900 border rounded-2xl p-5 flex flex-col justify-between h-full transition-colors duration-300 hover:border-indigo-500/30 hover:bg-zinc-800/80', isTerminated ? 'border-red-500/50 bg-red-950/20' : 'border-white/10']">
    <span v-if="isOnline" class="absolute top-4 right-4 w-2.5 h-2.5 rounded-full bg-emerald-400 shadow-md shadow-emerald-500/50 md:animate-pulse" :title="t('node_online')"></span>
    <span v-else class="absolute top-4 right-4 w-3 h-3 rounded-full bg-red-500/80" :title="t('node_offline')"></span>
    <div>
      <div class="flex justify-between items-start mb-4 pr-8">
        <div class="flex items-center space-x-3 min-w-0">
          <div class="p-2 rounded-xl bg-white/5 border border-white/10 shrink-0">
            <component :is="nodeIcon" class="w-6 h-6 text-indigo-300" />
          </div>
          <div class="flex items-center space-x-2 min-w-0">
            <h2 class="text-xl font-bold tracking-tight truncate">{{ node.name }}</h2>
            <button v-if="canRename" @click="showRenameModal = true" :title="t('node_rename_tt')"
              class="p-2 rounded-lg bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-400 hover:text-white transition-colors">
              <Pencil class="w-3.5 h-3.5" />
            </button>
            <button v-if="canDelete" @click="confirmDelete()" :title="t('node_delete_tt')"
              class="p-2 rounded-lg hover:bg-red-500/10 transition-colors">
              <svg class="w-4 h-4 text-zinc-500 hover:text-red-400 cursor-pointer transition-colors" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
              </svg>
            </button>
            <span v-if="isTerminated" class="text-xs font-bold uppercase tracking-wider px-2 py-0.5 rounded-lg bg-red-500/20 border border-red-500/40 text-red-300">{{ t('node_terminated') }}</span>
          </div>
        </div>
      </div>
      <div class="space-y-2 text-sm">
        <div class="flex justify-between items-baseline gap-2">
            <span class="text-zinc-500 text-xs uppercase">{{ t('common_lan_ip') }}</span>
            <span class="text-white font-medium truncate">{{ node.ip_lan || t('common_na') }}</span>
        </div>
        <div class="flex justify-between items-baseline gap-2">
            <span class="text-zinc-500 text-xs uppercase">{{ t('common_hostname') }}</span>
            <span class="text-white font-medium truncate">{{ node.hostname || t('common_na') }}</span>
        </div>
        <div class="flex justify-between items-baseline gap-2">
            <span class="text-zinc-500 text-xs uppercase">{{ t('common_vpn_server') }}</span>
            <span class="text-white font-medium truncate text-right">{{ node.active_server || t('common_none') }}<span v-if="node.active_engine" class="text-xs text-zinc-500"> ({{ node.active_engine }}{{ node.active_proto ? ' / ' + node.active_proto : '' }})</span></span>
        </div>
        <div class="flex justify-between items-center gap-2">
            <span class="text-zinc-500 text-xs uppercase">{{ t('common_sub_urls') }}</span>
            <template v-if="nodeSubUrls.length">
              <div class="flex items-center gap-2 min-w-0">
                <span class="text-xs text-zinc-400 truncate max-w-[140px] sm:max-w-[180px] inline-block align-bottom" :title="nodeSubUrls.join('\n')">{{ nodeSubUrls[0] }}<template v-if="nodeSubUrls.length > 1"> +{{ nodeSubUrls.length - 1 }}</template></span>
                <button @click="copySubUrls" :title="t('node_copy_sub_tt')"
                  class="p-1 rounded-md bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-400 hover:text-white transition-colors shrink-0">
                  <Copy class="w-3 h-3" />
                </button>
              </div>
            </template>
            <span v-else class="text-xs text-zinc-500">{{ t('common_not_set') }}</span>
        </div>
        <div class="flex justify-between items-baseline gap-2">
            <span class="text-zinc-500 text-xs uppercase">{{ t('common_last_seen') }}</span>
            <span class="text-white font-medium truncate">{{ isOnline ? t('node_just_now') : timeSince(node.last_seen) }}</span>
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
          <strong class="font-semibold" :class="statusColorClass">{{ node.pipeline_status || t('node_idle') }}</strong>
        </span>
        <span v-if="(node.available_servers || []).length" class="shrink-0 px-2 py-0.5 rounded-md text-[10px] font-semibold uppercase tracking-wide bg-indigo-500/15 border border-indigo-500/30 text-indigo-300">
          {{ node.available_servers.length }} {{ t('node_configs') }}
        </span>
        <ChevronRight class="w-4 h-4 shrink-0 text-zinc-600 group-hover:text-zinc-200 group-hover:translate-x-0.5 transition-all" />
      </button>

      <div class="mt-3 space-y-2">
        <div v-if="isReadOnly && !canManage" class="w-full flex items-center justify-center space-x-2 border border-dashed border-white/10 text-zinc-500 font-medium py-2 px-4 rounded-xl">
            <EyeOff class="w-4 h-4" />
            <span>{{ t('node_read_only') }}</span>
        </div>
        <template v-if="canManage">
            <div class="flex flex-wrap gap-2">
                <button v-if="canEditSubCard" @click="openSubModal" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                  <Link class="w-4 h-4 shrink-0" />
                  <span class="font-mono text-sm truncate min-w-0">[{{ t('node_manage_sub') }}]</span>
                </button>
                <button v-if="canSwitch && !isTerminated" @click="showSwitchModal = true" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                  <Shield class="w-4 h-4 shrink-0" />
                  <span class="font-mono text-sm truncate min-w-0">[{{ t('node_switch_vpn') }}]</span>
                </button>
            </div>
            <div class="flex flex-wrap gap-2">
                <button v-if="canViewNodeLogs" @click="openLogs" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                    <ScrollText class="w-4 h-4 shrink-0" />
                    <span class="font-mono text-sm truncate min-w-0">[{{ t('node_view_logs') }}]</span>
                </button>
                <button v-if="canSwitch" @click="showTaskQueueModal = true" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                  <Hourglass class="w-4 h-4 shrink-0" />
                  <span class="font-mono text-sm truncate min-w-0">[{{ t('node_task_queue') }} ({{ pendingCommandCount }})]</span>
                </button>
                <button v-if="canUpdateClient" @click="pushClientFiles" class="flex-1 min-w-[180px] flex items-center justify-center space-x-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 font-semibold py-2 px-4 min-h-[40px] rounded-xl transition-colors">
                  <RefreshCw class="w-4 h-4 shrink-0" />
                  <span class="font-mono text-sm truncate min-w-0">[{{ t('node_push_client') }}]</span>
                </button>
            </div>
            </template>
      </div>

      <div v-if="toast" :class="['fixed bottom-20 md:bottom-6 right-4 md:right-6 z-50 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl border', toastType === 'success' ? 'bg-emerald-950/95 md:bg-emerald-500/15 border-emerald-500/40 text-emerald-200' : 'bg-red-950/95 md:bg-red-500/15 border-red-500/40 text-red-200']">
        {{ toast }}
      </div>
    </div>

    <!-- Modals -->
    <div v-if="showRenameModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showRenameModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('node_rename_title') }}<span class="font-mono text-indigo-400">]</span></h2>
        <input v-model="newNodeName" type="text" :placeholder="t('node_rename_ph')" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showRenameModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
          <button type="button" @click="renameNode" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">{{ t('node_rename_btn') }}</button>
        </div>
      </div>
    </div>

    <div v-if="showDeleteModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showDeleteModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-red-500/40 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-2 tracking-tight text-red-300"><span class="font-mono text-red-400">[</span>{{ t('node_delete_title') }}<span class="font-mono text-red-400">]</span></h2>
        <p class="text-zinc-400 mb-6 text-sm">{{ t('node_delete_hint', { name: node.name }) }}</p>
        <button v-if="canSoftDelete" @click="softDeleteNode" class="w-full flex items-center justify-between px-4 py-3 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors text-left mb-3">
          <span>
            <span class="block font-semibold text-white">{{ t('node_soft_delete') }}</span>
            <span class="block text-xs text-zinc-400 mt-1">{{ t('node_soft_delete_desc') }}</span>
          </span>
        </button>
        <div v-if="canTerminate" class="rounded-xl border border-red-500/30 bg-red-900/10 p-4">
          <button @click="deleteChoice = 'terminate'" class="w-full flex items-center justify-between text-left">
            <span>
              <span class="block font-semibold text-red-300">{{ t('node_terminate') }}</span>
              <span class="block text-xs text-red-200/60 mt-1">{{ t('node_terminate_desc') }}</span>
            </span>
          </button>
          <template v-if="deleteChoice === 'terminate'">
            <p class="text-xs text-zinc-500 mt-3 mb-1">{{ t('node_terminate_confirm_hint', { word: 'TERMINATE' }) }}</p>
            <input v-model="terminateConfirm" type="text" placeholder="TERMINATE" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-red-500 focus:border-red-500/50">
          </template>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showDeleteModal = false; deleteChoice = ''; terminateConfirm = ''" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
          <button v-if="deleteChoice === 'terminate' && canTerminate" type="button" @click="terminateNode" :disabled="terminateConfirm !== 'TERMINATE'" class="px-4 py-2 bg-red-500/20 hover:bg-red-500/30 border border-red-500/40 text-red-200 rounded-xl transition-colors disabled:opacity-40 disabled:cursor-not-allowed">{{ t('node_terminate_btn') }}</button>
        </div>
      </div>
    </div>

    <div v-if="showTaskQueueModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showTaskQueueModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <div class="flex items-center justify-between mb-4">
          <h2 class="text-2xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('node_task_queue_title') }}<span class="font-mono text-indigo-400">]</span></h2>
          <button type="button" @click="showTaskQueueModal = false" class="px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg transition-colors">{{ t('close') }}</button>
        </div>
        <div v-if="node.pending_command" class="flex items-center justify-between bg-amber-500/10 border border-amber-500/30 rounded-lg px-3 py-2">
          <div class="min-w-0">
            <p class="text-xs text-amber-200 font-medium flex items-center space-x-2">
              <Hourglass class="w-3.5 h-3.5 shrink-0" />
              <span>{{ t('node_pending_task') }} {{ pendingTaskLabel }}</span>
            </p>
            <code class="block mt-1 text-xs text-zinc-400 break-all whitespace-pre-wrap">{{ node.pending_command }}</code>
          </div>
        </div>
        <div v-else class="text-sm text-zinc-500 py-6 text-center">{{ t('node_no_pending') }}</div>
        <div class="mt-6 flex justify-end space-x-4">
          <button v-if="node.pending_command" type="button" @click="cancelPendingCommand" class="px-4 py-2 bg-red-500/15 hover:bg-red-500/30 border border-red-500/30 text-red-200 rounded-xl transition-colors flex items-center space-x-2">
            <X class="w-4 h-4" />
            <span>{{ t('node_cancel_pending') }}</span>
          </button>
        </div>
      </div>
    </div>

    <div v-if="showSubModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showSubModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('node_sub_title') }}<span class="font-mono text-indigo-400">]</span></h2>
        <div v-if="subUrls.length" class="space-y-2 mb-4">
          <div v-for="(url, idx) in subUrls" :key="idx" class="flex items-center gap-2">
            <span class="flex-1 min-w-0 text-xs text-zinc-300 break-all bg-zinc-800 border border-white/10 rounded-lg px-3 py-2 font-mono">{{ url }}</span>
            <button type="button" @click="removeSubUrl(idx)" :title="t('node_remove_url_tt')"
              class="p-2 rounded-lg hover:bg-red-500/10 text-zinc-500 hover:text-red-400 transition-colors shrink-0">
              <X class="w-4 h-4" />
            </button>
          </div>
        </div>
        <div class="space-y-3">
          <input v-model="newSubUrl" type="text" placeholder="https://example.com/subscription" @keyup.enter="addSubUrl"
            class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
          <button type="button" @click="addSubUrl"
            class="w-full flex items-center justify-center space-x-2 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl text-sm text-zinc-300 transition-colors">
            <Plus class="w-4 h-4" />
            <span>{{ t('node_add_url') }}</span>
          </button>
          <p class="text-xs text-zinc-500">{{ t('node_sub_hint') }}</p>
        </div>
        <div class="mt-8 flex justify-end space-x-4">
          <button type="button" @click="showSubModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
          <button type="button" @click="updateSubUrls" :disabled="!subUrls.length" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors disabled:opacity-40 disabled:cursor-not-allowed">{{ t('node_update_url') }}</button>
        </div>
      </div>
    </div>

    <div v-if="showSwitchModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showSwitchModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-2 tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('node_switch_title') }}<span class="font-mono text-indigo-400">]</span></h2>
        <p class="text-zinc-400 mb-5">{{ t('node_currently') }} <strong>{{ node.active_server || t('common_none') }}</strong></p>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-2.5 mb-4">
          <button @click="switchTo('fastest')" class="flex items-center justify-center space-x-2 px-3 py-2.5 text-xs text-center leading-tight transition-all rounded-xl bg-white/5 hover:bg-white/10 border border-white/10 font-semibold text-zinc-200"><Zap class="w-4 h-4 text-indigo-400 shrink-0" /><span class="truncate min-w-0">{{ t('node_fastest') }}</span></button>
          <button @click="switchTo('balanced')" class="flex items-center justify-center space-x-2 px-3 py-2.5 text-xs text-center leading-tight transition-all rounded-xl bg-white/5 hover:bg-white/10 border border-white/10 font-semibold text-zinc-200"><Scale class="w-4 h-4 text-indigo-400 shrink-0" /><span class="truncate min-w-0">{{ t('node_balanced') }}</span></button>
        </div>
        <p class="text-xs text-zinc-500 mb-2">{{ t('node_avail_configs', { n: (node.available_servers || []).length }) }}</p>
        <div v-if="serverGroups.length" class="max-h-56 overflow-y-auto pr-1 space-y-3">
          <div v-for="group in serverGroups" :key="group.key">
            <p v-if="group.provider" class="text-xs font-semibold uppercase tracking-wider text-indigo-400 mb-1.5">{{ group.provider }}</p>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
              <button v-for="srv in group.servers" :key="srv" @click="switchTo(srv)"
                :class="['px-3 py-2.5 text-xs text-center leading-tight transition-all rounded-xl font-semibold truncate', srv === node.active_server ? 'bg-emerald-500/20 hover:bg-emerald-500/30 border border-emerald-500/40 text-emerald-100' : 'bg-white/5 hover:bg-white/10 border border-white/10']">
                {{ srv }}
              </button>
            </div>
          </div>
        </div>
        <p v-else class="text-sm text-zinc-500">{{ t('node_no_configs') }}</p>
        <div class="mt-8 flex justify-end"><button type="button" @click="showSwitchModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('close') }}</button></div>
      </div>
    </div>

    <div v-if="showLogsModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="closeLogs">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl flex flex-col">
        <div class="flex items-start justify-between gap-3 mb-4">
          <h2 class="text-xl sm:text-2xl font-bold tracking-tight min-w-0 truncate"><span class="font-mono text-indigo-400">[</span>{{ t('node_logs_title', { name: node.name }) }}<span class="font-mono text-indigo-400">]</span></h2>
          <button type="button" @click="closeLogs" class="px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg transition-colors shrink-0">{{ t('close') }}</button>
        </div>
        <div class="flex flex-wrap gap-2 items-center text-sm mb-3">
          <div class="flex flex-wrap gap-1.5 p-1 bg-zinc-950/80 rounded-2xl border border-white/5">
            <button v-for="c in logContainers" :key="c" @click="selectContainer(c)"
              :class="['px-3.5 py-1.5 rounded-xl text-xs font-medium transition-all cursor-pointer', logContainer === c ? 'bg-indigo-600 text-white shadow-md shadow-indigo-950/50' : 'text-zinc-400 hover:text-white hover:bg-white/5']">
              {{ c }}
            </button>
          </div>
          <div class="flex flex-wrap items-center gap-2 ml-auto">
            <label class="flex items-center gap-2 text-xs text-zinc-400 cursor-pointer select-none">
              <input type="checkbox" v-model="autoRefreshLogs" class="accent-indigo-500" />
              {{ t('node_auto_refresh') }}
            </label>
            <select v-model="logRefreshInterval" :disabled="!autoRefreshLogs"
              class="bg-black/40 border border-white/10 rounded-lg px-2 py-1 text-xs text-zinc-300 focus:outline-none disabled:opacity-40">
              <option :value="3000">3s</option>
              <option :value="5000">5s</option>
              <option :value="10000">10s</option>
              <option :value="30000">30s</option>
            </select>
            <button @click="fetchLogs" :title="t('node_refresh')"
              class="flex items-center gap-2 px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg text-sm text-white transition-colors">
              <RefreshCw :class="['w-4 h-4', isLoadingLogs ? 'animate-spin' : '']" />
              <span>{{ t('node_refresh') }}</span>
            </button>
            <button @click="copyLogs" :title="t('node_copy')"
              class="flex items-center gap-2 px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg text-sm text-white transition-colors">
              <Copy class="w-4 h-4" />
              <span>{{ t('node_copy') }}</span>
            </button>
          </div>
        </div>
        <div :class="['terminal font-mono text-xs p-4 rounded-lg h-64 sm:h-96 min-h-0 overflow-y-auto whitespace-pre-wrap flex-1 border', prefs.theme_mode === 'light' ? 'bg-zinc-100 text-zinc-900 border-zinc-300' : 'bg-zinc-950 text-emerald-400 border-white/10']" ref="logHost">
          <div v-if="isLoadingLogs && !nodeLogs" class="flex items-center justify-center h-full text-zinc-400">{{ t('node_loading_logs') }}</div>
          <pre v-else>{{ nodeLogs || t('node_no_logs') }}</pre>
        </div>
      </div>
    </div>

    <div v-if="activeModal === 'status'" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="activeModal = ''">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <div class="flex items-center justify-between mb-6">
          <h2 class="text-2xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('node_status_title') }}<span class="font-mono text-indigo-400">]</span></h2>
          <button type="button" @click="activeModal = ''" class="px-3 py-1.5 bg-white/5 hover:bg-white/10 border border-white/10 rounded-lg transition-colors">{{ t('close') }}</button>
        </div>
        <div class="space-y-5 text-sm">
          <div>
            <p class="text-xs uppercase tracking-wider text-zinc-500 mb-1">{{ t('node_pipeline_status') }}</p>
            <p class="text-base font-semibold" :class="statusColorClass">{{ node.pipeline_status || t('node_idle') }}</p>
          </div>
          <div>
            <p class="text-xs uppercase tracking-wider text-zinc-500 mb-1">{{ t('node_message') }}</p>
            <p class="text-zinc-300 leading-relaxed break-words whitespace-pre-wrap">{{ node.status_message || t('node_no_message') }}</p>
          </div>
          <div>
            <p class="text-xs uppercase tracking-wider text-zinc-500 mb-1">{{ t('node_pending_command') }}</p>
            <code class="block text-xs text-zinc-400 break-all whitespace-pre-wrap">{{ node.pending_command ? node.pending_command : t('common_none') }}</code>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, inject, watch, onMounted, onUnmounted, nextTick } from 'vue';
import axios from 'axios';
import { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Pencil, Copy, EyeOff, ScrollText, X, ChevronRight, Zap, Scale, Plus } from 'lucide-vue-next';

const ONLINE_THRESHOLD_SECONDS = 90;

export default {
  name: 'NodeCard',
  components: { Server, Cpu, RefreshCw, Shield, Hourglass, CheckCircle2, XCircle, ArrowDown, Cog, Link, Pencil, Copy, EyeOff, ScrollText, X, ChevronRight, Zap, Scale, Plus },
  props: {
    node: {
      type: Object,
      required: true,
    },
  },
  emits: ['node-updated', 'node-deleted'],
  setup(props, { emit }) {
    const t = inject('t') || ((k) => k);
    const prefs = inject('prefs', ref({ theme_mode: 'obsidian' }));
    const modalBackdrop = computed(() => prefs.value.theme_mode === 'light' ? 'bg-zinc-900/70 backdrop-blur-md' : 'bg-black/70 backdrop-blur-md');
    const { user, hasPermission, isReadOnly } = inject('authCtx', { user: ref(null), hasPermission: () => false, isReadOnly: ref(false) });

    const isOwner = computed(() => user.value?.role?.name === 'owner' || user.value?.role === 'owner' || user.value?.username === 'owner' || user.value?.username === 'admin');
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
    const subUrls = ref([]);
    const nodeSubUrls = computed(() => props.node.sub_urls || (props.node.sub_url ? [props.node.sub_url] : []));

    const openSubModal = () => {
      subUrls.value = [...nodeSubUrls.value];
      newSubUrl.value = '';
      showSubModal.value = true;
    };
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
        if (!dateStr) return t('node_never');
        const date = new Date(dateStr);
        const seconds = Math.floor((new Date() - date) / 1000);
        if (seconds < 5) return t('node_just_now');
        if (seconds < 60) return seconds === 1 ? t('time_second_ago') : t('time_seconds_ago', { n: seconds });
        const minutes = Math.floor(seconds / 60);
        if (minutes < 60) return minutes === 1 ? t('time_minute_ago') : t('time_minutes_ago', { n: minutes });
        const hours = Math.floor(minutes / 60);
        if (hours < 24) return hours === 1 ? t('time_hour_ago') : t('time_hours_ago', { n: hours });
        const days = Math.floor(hours / 24);
        return days === 1 ? t('time_day_ago') : t('time_days_ago', { n: days });
    };

    const showToast = (msg, type = 'success', duration = 4000) => {
      toast.value = msg;
      toastType.value = type;
      clearTimeout(toastTimer);
      toastTimer = setTimeout(() => { toast.value = ''; }, duration);
    };

    const addSubUrl = () => {
      const raw = newSubUrl.value || '';
      const parts = raw.split(/[\s,]+/).map(s => s.trim()).filter(Boolean);
      if (!parts.length) return;
      const known = new Set(subUrls.value);
      parts.forEach(u => { if (!known.has(u)) { known.add(u); subUrls.value.push(u); } });
      newSubUrl.value = '';
    };

    const removeSubUrl = (idx) => {
      subUrls.value.splice(idx, 1);
    };

    const updateSubUrls = async () => {
      if (!subUrls.value.length) {
        showToast(t('node_toast_sub_required'), 'error');
        return;
      }
      try {
        await axios.put(`/api/web/nodes/${props.node.id}/sub`, { sub_urls: subUrls.value });
        showSubModal.value = false;
        emit('node-updated');
        showToast(t('node_toast_sub_updated'));
      } catch (e) {
        console.error('Error updating sub URLs:', e);
        if (e.response && e.response.status === 400 && e.response.data && e.response.data.error && e.response.data.error.startsWith('Invalid Subscription URL')) {
          showToast(t('node_sub_invalid_url'), 'error');
        } else {
          showToast(t('node_toast_sub_failed'), 'error');
        }
      }
    };

    const pushClientFiles = async () => {
      if (!confirm(t('node_push_confirm', { name: props.node.name }))) return;
      try {
        await axios.post('/api/web/nodes/update-client-files', { node_id: props.node.id });
        emit('node-updated');
        showToast(t('node_toast_push_queued'));
      } catch (e) {
        console.error('Error pushing client files:', e);
        showToast(t('node_toast_push_failed'), 'error');
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
        showToast(t('node_toast_deleted'));
      } catch (e) {
        console.error('Error deleting node:', e);
        showToast(t('node_toast_delete_failed'), 'error');
      }
    };

    const showSwitchModal = ref(false);

    const providerNames = ref({});
    const fetchProviderNames = async () => {
      try {
        const response = await axios.get('/api/web/providers');
        if (response.status === 200 && Array.isArray(response.data)) {
          const map = {};
          response.data.forEach(p => { if (p.domain) map[p.domain] = p.name || p.domain; });
          providerNames.value = map;
        }
      } catch (e) {
        console.error('Error fetching providers:', e);
      }
    };

    const serverGroups = computed(() => {
      const servers = props.node.available_servers || [];
      const sp = props.node.server_providers || {};
      const byProvider = {};
      servers.forEach(srv => {
        const dom = sp[srv] || '';
        const key = dom || '__none__';
        if (!byProvider[key]) {
          byProvider[key] = {
            key,
            provider: key === '__none__' ? null : (providerNames.value[dom] || dom),
            servers: [],
          };
        }
        byProvider[key].servers.push(srv);
      });
      return Object.values(byProvider);
    });

    const switchTo = async (target) => {
      try {
        await axios.post(`/api/web/nodes/${props.node.id}/command`, { command: `switch:${target}` });
        showSwitchModal.value = false;
        emit('node-updated');
        showToast(t('node_toast_switch_queued', { target }));
      } catch (e) {
        console.error('Error switching:', e);
        showToast(t('node_toast_switch_failed'), 'error');
      }
    };

    const copySubUrls = async () => {
      const urls = nodeSubUrls.value;
      if (!urls.length) return;
      try {
        await navigator.clipboard.writeText(urls.join('\n'));
        showToast(t('node_toast_sub_copied'));
      } catch (e) {
        console.error('Failed to copy sub URLs:', e);
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
        showToast(t('node_toast_terminate_queued'));
      } catch (e) {
        console.error('Error queueing terminate:', e);
        showToast(t('node_toast_terminate_failed'), 'error');
      }
    };

    const cancelPendingCommand = async () => {
      try {
        await axios.put(`/api/web/nodes/${props.node.id}/clear-command`);
        props.node.pending_command = '';
        props.node.pending_msg_id = '';
        showTaskQueueModal.value = false;
        emit('node-updated');
        showToast(t('node_toast_task_cancelled'));
      } catch (e) {
        console.error('Error clearing pending command:', e);
        showToast(t('node_toast_task_cancel_failed'), 'error');
      }
    };

    const renameNode = async () => {
      if (!newNodeName.value) return;
      try {
        await axios.put(`/api/web/nodes/${props.node.id}/rename`, { name: newNodeName.value });
        showRenameModal.value = false;
        emit('node-updated');
        showToast(t('node_toast_renamed'));
      } catch (e) {
        console.error('Error renaming node:', e);
        showToast(t('node_toast_rename_failed'), 'error');
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
        if (response.data.error) {
          nodeLogs.value = `[${t('node_logs_failed')}] ${response.data.error}`;
        } else {
          nodeLogs.value = response.data.logs || t('node_logs_empty', { container: logContainer.value });
        }
        scrollLogsToBottom();
      } catch (e) {
        console.error('Error fetching logs:', e);
        nodeLogs.value = t('node_logs_failed');
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
        showToast(t('node_toast_logs_copied'));
      } catch (e) {
        console.error('Failed to copy logs:', e);
      }
    };

    onMounted(() => {
      fetchProviderNames();
    });

    onUnmounted(() => {
      stopAutoRefresh();
    });

    return {
      isOnline, isTerminated, nodeIcon, pipelineStatusIcon, timeSince, statusColorClass, statusBgClass, pendingTaskLabel, pendingCommandCount,
      showSubModal, newSubUrl, subUrls, nodeSubUrls, openSubModal, addSubUrl, removeSubUrl, updateSubUrls,
      pushClientFiles,
      copySubUrls, softDeleteNode, confirmDelete,
      showDeleteModal, deleteChoice,
      showTaskQueueModal,
      activeModal,
      showSwitchModal, switchTo, serverGroups,
      showRenameModal, newNodeName, renameNode,
      showTerminateModal, terminateConfirm, terminateNode,
      cancelPendingCommand,
      showLogsModal, nodeLogs, isLoadingLogs, openLogs, closeLogs, fetchLogs, selectContainer,
      logContainers, logContainer, autoRefreshLogs, logRefreshInterval, copyLogs,
      user, isOwner, canManage, canRename, canDelete, canEditSubCard, canSwitch, canViewNodeLogs,
      canTerminate, canUpdateClient, canSoftDelete,
      isReadOnly, toast, toastType, logHost, prefs, t,
    };
  },
};
</script>