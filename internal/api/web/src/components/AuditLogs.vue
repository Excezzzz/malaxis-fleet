<template>
  <div>
    <div class="flex justify-between items-center mb-8">
      <h1 class="text-4xl font-bold tracking-tight">Audit Logs</h1>
      <button @click="exportLogs" class="flex items-center space-x-2 px-4 py-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">
        <Download class="w-5 h-5" />
        <span>Export Logs</span>
      </button>
    </div>

    <div class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl overflow-hidden">
      <table class="min-w-full divide-y divide-white/5">
        <thead class="bg-white/[0.03]">
          <tr>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">Timestamp</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">Actor</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">Action</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">Target</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-500 uppercase tracking-wider">Details</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-white/5">
          <tr v-for="log in logs" :key="log.id" class="hover:bg-white/[0.03] transition-colors">
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-400">{{ new Date(log.timestamp).toLocaleString() }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-white">{{ log.actor }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-300">{{ log.action }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-400">{{ log.target || 'N/A' }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-500">{{ log.details }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script>
import { ref, onMounted } from 'vue';
import { Download } from 'lucide-vue-next';

export default {
  name: 'AuditLogs',
  components: { Download },
  setup() {
    const logs = ref([]);

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

    onMounted(() => {
      fetchLogs();
    });

    const exportLogs = async () => {
      try {
        const response = await fetch('/api/web/audit');
        if (!response.ok) throw new Error('Failed to fetch logs');
        const data = await response.json();
        const rows = [['Timestamp', 'Actor', 'Action', 'Target', 'Details']];
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
        alert('Could not export logs.');
      }
    };

    return {
      logs,
      exportLogs,
    };
  },
};
</script>
