<template>
  <div>
    <div class="flex flex-wrap justify-between items-center mb-8 gap-4">
      <div>
        <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>Client Files<span class="font-mono text-indigo-400">]</span></h1>
        <p class="text-zinc-500 mt-1 text-sm">Deploy templates served to fleet agents at <span class="text-indigo-300 font-mono">/node_agent.py</span>, <span class="text-indigo-300 font-mono">/fleet-cli.sh</span> and friends. Edit a file, save it, then push to devices.</p>
      </div>
      <button @click="pushFiles" :disabled="pushing"
        :class="['flex items-center space-x-2 px-5 py-2.5 rounded-xl font-semibold transition-all duration-300 border', pushing ? 'bg-white/5 border-white/10 text-zinc-400 cursor-wait' : 'bg-indigo-500/15 hover:bg-indigo-500/25 border-indigo-500/30 text-indigo-100']">
        <Rocket v-if="!pushing" class="w-5 h-5" />
        <RefreshCw v-else class="w-5 h-5 animate-spin" />
        <span class="font-mono text-sm">{{ pushing ? '[Queuing...]' : '[Push Latest Client Files to Devices]' }}</span>
      </button>
    </div>

    <div v-if="error" class="bg-red-900/20 border border-red-500/30 text-red-300 px-4 py-3 rounded-xl mb-6" role="alert">
      <strong class="font-bold">Error:</strong>
      <span class="block sm:inline">{{ error }}</span>
    </div>

    <div v-if="toast" class="fixed bottom-6 right-6 z-50 bg-emerald-500/15 border border-emerald-500/40 text-emerald-200 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl">
      {{ toast }}
    </div>

    <div v-if="files.length" class="grid grid-cols-1 lg:grid-cols-5 gap-6">
      <div class="lg:col-span-1 space-y-2">
        <button v-for="f in files" :key="f.name" @click="selectFile(f.name)"
          :class="['w-full flex items-center justify-between px-4 py-3 rounded-2xl border transition-all duration-300 text-left', selected === f.name ? 'bg-zinc-800/60 border-indigo-500/40 text-white' : 'bg-zinc-900/40 border-white/5 text-zinc-400 hover:border-indigo-500/30 hover:text-white']">
          <span class="flex items-center space-x-2 truncate">
            <FileCode2 class="w-4 h-4 shrink-0" />
            <span class="font-mono text-sm truncate">{{ f.name }}</span>
          </span>
          <span class="text-xs text-zinc-500 shrink-0">{{ f.content.length.toLocaleString() }} B</span>
        </button>
      </div>

      <div class="lg:col-span-4 bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl overflow-hidden flex flex-col">
        <div class="flex items-center justify-between px-5 py-3 border-b border-white/5 bg-zinc-900/60">
          <span class="font-mono text-sm text-indigo-300">{{ selected }}</span>
          <div class="flex items-center space-x-3">
            <span class="text-xs text-zinc-500">{{ lines.length }} lines</span>
            <span class="text-xs" :class="dirty ? 'text-amber-300' : 'text-zinc-500'">{{ dirty ? 'Unsaved changes' : 'Saved' }}</span>
            <button @click="saveFile" :disabled="saving"
              :class="['flex items-center space-x-2 px-4 py-1.5 rounded-xl text-sm font-semibold border transition-all duration-300', saving ? 'bg-white/5 border-white/10 text-zinc-400 cursor-wait' : 'bg-indigo-500/15 hover:bg-indigo-500/25 border-indigo-500/30 text-indigo-100']">
              <Save v-if="!saving" class="w-4 h-4" />
              <RefreshCw v-else class="w-4 h-4 animate-spin" />
              <span class="font-mono text-sm">{{ saving ? '[Saving...]' : '[Save File]' }}</span>
            </button>
          </div>
        </div>
        <textarea v-model="content" spellcheck="false"
          class="p-5 flex-1 min-h-[70vh] bg-transparent text-xs leading-relaxed font-mono text-zinc-300 resize-none focus:outline-none"
          @input="markDirty"></textarea>
      </div>
    </div>

    <div v-else-if="!error" class="text-center py-16">
      <p class="text-zinc-500 text-lg">No template files found on the server.</p>
    </div>
  </div>
</template>

<script>
import { ref, computed, onMounted } from 'vue';
import axios from 'axios';
import { FileCode2, Rocket, RefreshCw, Save } from 'lucide-vue-next';

export default {
  name: 'ClientFiles',
  components: { FileCode2, Rocket, RefreshCw, Save },
  setup() {
    const files = ref([]);
    const selected = ref('');
    const content = ref('');
    const error = ref(null);
    const pushing = ref(false);
    const saving = ref(false);
    const dirty = ref(false);
    const toast = ref('');
    let toastTimer = null;

    const showToast = (msg) => {
      toast.value = msg;
      clearTimeout(toastTimer);
      toastTimer = setTimeout(() => { toast.value = ''; }, 4000);
    };

    const fetchTemplates = async () => {
      try {
        const response = await axios.get('/api/web/templates');
        files.value = response.data.files || [];
        if (files.value.length && !selected.value) {
          selected.value = files.value[0].name;
        }
        const current = files.value.find(f => f.name === selected.value);
        if (current) content.value = current.content;
        error.value = null;
        dirty.value = false;
      } catch (e) {
        console.error("Failed to fetch templates:", e);
        error.value = e.message;
      }
    };

    const selectFile = (name) => {
      if (dirty.value && !confirm('Discard unsaved changes to this file?')) return;
      selected.value = name;
      const current = files.value.find(f => f.name === selected.value);
      if (current) content.value = current.content;
      dirty.value = false;
    };

    const markDirty = () => {
      dirty.value = true;
    };

    const current = computed(() => files.value.find(f => f.name === selected.value) || null);
    const lines = computed(() => content.value.replace(/\r\n/g, '\n').split('\n'));

    const saveFile = async () => {
      if (!current.value || saving.value) return;
      saving.value = true;
      error.value = null;
      try {
        const response = await axios.put(`/api/web/templates/${encodeURIComponent(selected.value)}`, {
          content: content.value,
        });
        if (response.data.status !== 'ok') throw new Error('Save failed');
        current.value.content = content.value;
        dirty.value = false;
        showToast(`Saved ${selected.value} — the change is live at the download URLs.`);
      } catch (e) {
        console.error("Failed to save template:", e);
        error.value = e.message;
        showToast('Could not save the file.');
      } finally {
        saving.value = false;
      }
    };

    const pushFiles = async () => {
      if (!confirm('Queue "update_client_files" for ALL devices?\n\nAgents will download the latest templates and gracefully restart with the new code.')) return;
      pushing.value = true;
      error.value = null;
      try {
        const response = await axios.post('/api/web/nodes/update-client-files', {});
        if (response.data.status !== 'ok') throw new Error('Push failed');
        showToast(`Queued client file update for ${response.data.commands_queued} node(s).`);
      } catch (e) {
        console.error("Failed to push client files:", e);
        error.value = e.message;
        showToast('Could not queue client file update.');
      } finally {
        pushing.value = false;
      }
    };

    onMounted(fetchTemplates);

    return {
      files,
      selected,
      content,
      error,
      pushing,
      saving,
      dirty,
      toast,
      fetchTemplates,
      selectFile,
      markDirty,
      current,
      lines,
      saveFile,
      pushFiles,
    };
  },
};
</script>
