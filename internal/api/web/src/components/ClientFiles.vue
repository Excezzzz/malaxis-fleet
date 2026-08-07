<template>
  <div>
    <div class="flex flex-wrap justify-between items-center mb-8 gap-4">
      <div>
        <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('client_files_title') }}<span class="font-mono text-indigo-400">]</span></h1>
        <p class="text-zinc-500 mt-1 text-sm">{{ t('client_files_subtitle') }} <span class="text-indigo-300 font-mono">/node_agent.py</span>, <span class="text-indigo-300 font-mono">/fleet-cli.sh</span>.</p>
      </div>
      <button @click="pushFiles" :disabled="pushing"
        :class="['flex items-center space-x-2 px-5 py-2.5 rounded-xl font-semibold transition-all duration-300 border', pushing ? 'bg-white/5 border-white/10 text-zinc-400 cursor-wait' : 'bg-indigo-500/15 hover:bg-indigo-500/25 border-indigo-500/30 text-indigo-100']">
        <Rocket v-if="!pushing" class="w-5 h-5 shrink-0" />
        <RefreshCw v-else class="w-5 h-5 shrink-0 animate-spin" />
        <span class="font-mono text-sm truncate min-w-0">{{ pushing ? `[${t('client_push_queued')}]` : `[${t('client_push')}]` }}</span>
      </button>
    </div>

    <div v-if="error" class="bg-red-900/20 border border-red-500/30 text-red-300 px-4 py-3 rounded-xl mb-6" role="alert">
      <strong class="font-bold">{{ t('client_error_label') }}:</strong>
      <span class="block sm:inline">{{ error }}</span>
    </div>

    <div class="bg-zinc-900/40 backdrop-blur-md border border-indigo-500/20 rounded-2xl p-5 mb-6">
      <div class="flex flex-wrap items-center justify-between gap-2 mb-3">
        <div>
          <h2 class="text-lg font-bold text-white">{{ t('client_add_device') }}</h2>
          <p class="text-sm text-zinc-400">{{ t('client_add_device_hint') }}</p>
        </div>
      </div>
      <div class="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 w-full">
        <code v-if="installCommand" class="terminal w-full sm:flex-1 bg-zinc-950 text-emerald-400 p-3.5 rounded-xl font-mono text-xs overflow-x-auto border border-white/10 break-all whitespace-pre-wrap">{{ installCommand }}</code>
        <div v-else :class="['terminal w-full sm:flex-1 bg-zinc-950 p-3.5 rounded-xl font-mono text-xs overflow-x-auto border border-white/10', prefs.theme_mode === 'light' ? 'text-zinc-900' : 'text-zinc-400']">
          {{ commandError || t('client_loading_cmd') }}
        </div>
        <button @click="copyInstallCommand" :disabled="!installCommand"
          class="w-full sm:w-auto shrink-0 py-2.5 px-4 bg-zinc-800 hover:bg-zinc-700 text-white font-medium text-xs rounded-xl flex items-center justify-center gap-2 border border-white/10 transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed">
          <Copy v-if="!copied" class="w-4 h-4 shrink-0" />
          <Check v-else class="w-4 h-4 shrink-0" />
          <span class="truncate min-w-0">{{ copied ? `[${t('client_copied')}]` : `[${t('client_copy')}]` }}</span>
        </button>
      </div>
    </div>

    <div v-if="toast" class="fixed bottom-20 md:bottom-6 right-4 md:right-6 z-50 bg-emerald-500/15 border border-emerald-500/40 text-emerald-200 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl">
      {{ toast }}
    </div>

    <div v-if="files.length" class="grid grid-cols-1 lg:grid-cols-5 gap-4">
      <div class="lg:col-span-1 space-y-2 w-full">
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
        <div class="flex flex-wrap items-center justify-between gap-2 px-5 py-3 border-b border-white/5 bg-zinc-900/60">
          <span class="font-mono text-sm text-indigo-300 min-w-0 truncate">{{ selected }}</span>
          <div class="flex flex-wrap items-center gap-3">
            <span class="text-xs text-zinc-500">{{ lines.length }} {{ t('client_lines') }}</span>
            <span class="text-xs" :class="dirty ? 'text-amber-300' : 'text-zinc-500'">{{ dirty ? t('client_unsaved') : t('client_saved') }}</span>
            <button @click="saveFile" :disabled="saving"
              :class="['flex items-center space-x-2 px-4 py-1.5 rounded-xl text-sm font-semibold border transition-all duration-300', saving ? 'bg-white/5 border-white/10 text-zinc-400 cursor-wait' : 'bg-indigo-500/15 hover:bg-indigo-500/25 border-indigo-500/30 text-indigo-100']">
              <Save v-if="!saving" class="w-4 h-4" />
              <RefreshCw v-else class="w-4 h-4 animate-spin" />
              <span class="font-mono text-sm truncate min-w-0">{{ saving ? `[${t('client_saving')}]` : `[${t('client_save')}]` }}</span>
            </button>
          </div>
        </div>
        <textarea v-model="content" spellcheck="false"
          :class="['terminal p-5 flex-1 min-h-[70vh] text-xs leading-relaxed font-mono resize-none focus:outline-none overflow-x-auto whitespace-pre', prefs.theme_mode === 'light' ? 'bg-zinc-100 text-zinc-900' : 'bg-zinc-950 text-zinc-100']"
          @input="markDirty"></textarea>
      </div>
    </div>

    <div v-else-if="!error" class="text-center py-16">
      <p class="text-zinc-500 text-lg">{{ t('client_no_templates') }}</p>
    </div>
  </div>
</template>

<script>
import { ref, computed, onMounted, inject } from 'vue';
import axios from 'axios';
import { FileCode2, Rocket, RefreshCw, Save, Copy, Check } from 'lucide-vue-next';

export default {
  name: 'ClientFiles',
  components: { FileCode2, Rocket, RefreshCw, Save, Copy, Check },
  setup() {
    const t = inject('t') || ((k) => k);
    const prefs = inject('prefs', ref({ theme_mode: 'obsidian' }));
    const files = ref([]);
    const selected = ref('');
    const content = ref('');
    const error = ref(null);
    const pushing = ref(false);
    const saving = ref(false);
    const dirty = ref(false);
    const toast = ref('');
    const installCommand = ref('');
    const commandError = ref('');
    const copied = ref(false);
    let toastTimer = null;

    const showToast = (msg) => {
      toast.value = msg;
      clearTimeout(toastTimer);
      toastTimer = setTimeout(() => { toast.value = ''; }, 4000);
    };

    const copyInstallCommand = async () => {
      if (!installCommand.value) return;
      try {
        await navigator.clipboard.writeText(installCommand.value);
        copied.value = true;
        setTimeout(() => { copied.value = false; }, 2000);
      } catch (e) {
        showToast(t('client_toast_copy_failed'));
      }
    };

    const fetchInstallCommand = async () => {
      try {
        const response = await axios.get('/api/web/install-command');
        installCommand.value = response.data.command || '';
        commandError.value = '';
      } catch (e) {
        console.error("Failed to fetch install command:", e);
        commandError.value = t('client_cmd_error');
      }
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
      if (dirty.value && !confirm(t('client_confirm_discard'))) return;
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
        showToast(t('client_saved_toast', { name: selected.value }));
      } catch (e) {
        console.error("Failed to save template:", e);
        error.value = e.message;
        showToast(t('client_save_failed'));
      } finally {
        saving.value = false;
      }
    };

    const pushFiles = async () => {
      if (!confirm(t('client_push_confirm'))) return;
      pushing.value = true;
      error.value = null;
      try {
        const response = await axios.post('/api/web/nodes/update-client-files', {});
        if (response.data.status !== 'ok') throw new Error('Push failed');
        showToast(t('client_push_queued_toast', { n: response.data.commands_queued }));
      } catch (e) {
        console.error("Failed to push client files:", e);
        error.value = e.message;
        showToast(t('client_push_failed_toast'));
      } finally {
        pushing.value = false;
      }
    };

    onMounted(() => {
      fetchInstallCommand();
      fetchTemplates();
    });

    return {
      files,
      selected,
      content,
      error,
      pushing,
      saving,
      dirty,
      toast,
      installCommand,
      commandError,
      copied,
      copyInstallCommand,
      fetchTemplates,
      selectFile,
      markDirty,
      current,
      lines,
      saveFile,
      pushFiles,
      prefs,
      t,
    };
  },
};
</script>
