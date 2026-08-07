<template>
  <div>
    <div class="flex flex-wrap justify-between items-center gap-3 mb-8">
      <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('logs_title') }}<span class="font-mono text-indigo-400">]</span></h1>
      <div v-if="activeTab === 'audit'" class="flex items-center space-x-3">
        <button @click="exportLogs" class="flex items-center space-x-2 px-4 py-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">
          <Download class="w-5 h-5" />
          <span class="font-mono text-sm">[{{ t('audit_export') }}]</span>
        </button>
      </div>
    </div>

    <div class="flex rounded-xl bg-white/5 border border-white/10 p-1 mb-8 w-fit">
      <button v-if="canViewAuditLogs" @click="activeTab = 'audit'"
        :class="['px-5 py-2 text-sm font-semibold rounded-lg transition-colors', activeTab === 'audit' ? 'bg-indigo-500/25 text-white' : 'text-zinc-400 hover:text-white hover:bg-white/5']">
        {{ t('audit_trail') }}
      </button>
      <button v-if="canViewMasterLogs" @click="activeTab = 'master'"
        :class="['px-5 py-2 text-sm font-semibold rounded-lg transition-colors', activeTab === 'master' ? 'bg-indigo-500/25 text-white' : 'text-zinc-400 hover:text-white hover:bg-white/5']">
        {{ t('audit_master') }}
      </button>
    </div>

    <div v-if="toast" class="fixed bottom-20 md:bottom-6 right-4 md:right-6 z-50 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl border bg-emerald-500/15 border-emerald-500/40 text-emerald-200">
      {{ toast }}
    </div>

    <div v-if="activeTab === 'audit'" class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl overflow-x-auto">
      <table class="min-w-full divide-y divide-white/5">
        <thead class="bg-white/[0.03]">
          <tr>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">{{ t('logs_th_timestamp') }}</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">{{ t('logs_th_actor') }}</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">{{ t('logs_th_action') }}</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">{{ t('logs_th_target') }}</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">{{ t('logs_th_details') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-white/5">
          <tr v-for="log in logs" :key="log.id" class="hover:bg-white/[0.03] transition-colors">
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-400">{{ new Date(log.timestamp).toLocaleString() }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-white">{{ log.actor }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-300">{{ log.action }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-400">{{ log.target || t('common_na') }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-500">{{ log.details }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else>
      <div class="flex flex-wrap justify-between items-center mb-4 gap-4">
        <div class="flex items-center gap-3">
          <h2 class="text-2xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('logs_master_title') }}<span class="font-mono text-indigo-400">]</span></h2>
          <select v-model="masterContainer" @change="fetchMasterLogs"
            class="bg-zinc-900/60 border border-white/10 rounded-xl px-3 py-1.5 text-sm text-zinc-300 focus:outline-none">
            <option v-for="c in masterContainers" :key="c" :value="c">{{ c }}</option>
          </select>
        </div>
        <div class="flex items-center gap-3">
          <button @click="fetchMasterLogs" class="flex items-center space-x-2 px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white rounded-xl transition-colors">
            <RefreshCw :class="['w-4 h-4', isLoadingMasterLogs ? 'animate-spin' : '']" />
            <span class="font-mono text-sm">[{{ t('logs_refresh') }}]</span>
          </button>
          <button @click="copyMasterLogs" class="flex items-center space-x-2 px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 text-white rounded-xl transition-colors">
            <Copy class="w-4 h-4" />
            <span class="font-mono text-sm">[{{ t('logs_copy') }}]</span>
          </button>
        </div>
      </div>
      <div class="terminal bg-black p-4 rounded-2xl border border-white/5 font-mono text-xs text-emerald-400 h-[36rem] overflow-y-auto whitespace-pre-wrap shadow-inner">
        <div v-if="isLoadingMasterLogs && !masterLogs" class="flex items-center justify-center h-full text-zinc-400">{{ t('logs_loading') }}</div>
        <pre v-else>{{ masterLogs || t('logs_none') }}</pre>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, inject, onMounted } from 'vue';
import { Download, RefreshCw, Copy } from 'lucide-vue-next';

export default {
  name: 'AuditLogs',
  components: { Download, RefreshCw, Copy },
  setup() {
    const authCtx = inject('authCtx', {});
    const t = inject('t') || ((k) => k);
    const canViewAuditLogs = computed(() => authCtx.canViewAuditLogs?.value ?? false);
    const canViewMasterLogs = computed(() => authCtx.canViewMasterLogs?.value ?? false);

    const logs = ref([]);
    const activeTab = ref(canViewAuditLogs.value ? 'audit' : 'master');
    const masterLogs = ref('');
    const isLoadingMasterLogs = ref(false);
    const masterContainers = ['fleet-master', 'fleet-postgres'];
    const masterContainer = ref('fleet-master');
    const toast = ref('');
    let toastTimer = null;

    const showToast = (msg, type = 'success', duration = 4000) => {
      toast.value = msg;
      clearTimeout(toastTimer);
      toastTimer = setTimeout(() => { toast.value = ''; }, duration);
    };

    const fetchLogs = async () => {
      try {
        const response = await fetch('/api/web/audit');
        if (response.ok) {
          logs.value = await response.json();
        }
      } catch (e) {
        console.error('Error fetching audit logs:', e);
      }
    };

    const fetchMasterLogs = async () => {
      isLoadingMasterLogs.value = true;
      try {
        const response = await fetch(`/api/web/logs/master?container=${encodeURIComponent(masterContainer.value)}`);
        if (!response.ok) throw new Error('Failed to fetch master logs');
        const data = await response.json();
        masterLogs.value = data.logs || '';
      } catch (e) {
        console.error('Error fetching master logs:', e);
        masterLogs.value = t('logs_failed');
      } finally {
        isLoadingMasterLogs.value = false;
      }
    };

    const copyMasterLogs = async () => {
      if (!masterLogs.value) return;
      try {
        await navigator.clipboard.writeText(masterLogs.value);
        showToast(t('logs_copied'));
      } catch (e) {
        console.error('Failed to copy master logs:', e);
      }
    };

    onMounted(() => {
      if (canViewAuditLogs.value) fetchLogs();
      if (canViewMasterLogs.value) fetchMasterLogs();
    });

    const exportLogs = async () => {
      try {
        const response = await fetch('/api/web/audit');
        if (!response.ok) throw new Error('Failed to fetch logs');
        const data = await response.json();
        const rows = [[t('logs_th_timestamp'), t('logs_th_actor'), t('logs_th_action'), t('logs_th_target'), t('logs_th_details')]];
        for (const log of data) {
          rows.push([
            new Date(log.timestamp).toISOString(),
            log.actor || '',
            log.action || '',
            log.target || '',
            log.details || '',
          ]);
        }
        const csv = rows.map(r => r.map(c => `"${String(c).replace(/"/g, '""')}"`).join(',')).join('\n');
        const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `audit_logs_${new Date().toISOString().slice(0, 10)}.csv`;
        a.click();
        URL.revokeObjectURL(url);
      } catch (e) {
        console.error('Error exporting logs:', e);
        alert(t('logs_export_failed'));
      }
    };

    return {
      logs,
      activeTab,
      exportLogs,
      masterLogs,
      isLoadingMasterLogs,
      fetchMasterLogs,
      copyMasterLogs,
      masterContainers,
      masterContainer,
      toast,
      canViewAuditLogs,
      canViewMasterLogs,
      t,
    };
  },
};
</script>
