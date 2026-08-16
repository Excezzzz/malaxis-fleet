<template>
  <div>
    <div class="flex flex-wrap justify-between items-center gap-3 mb-8">
      <div>
        <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('providers_title') }}<span class="font-mono text-indigo-400">]</span></h1>
        <p class="text-zinc-500 mt-2 max-w-2xl">{{ t('providers_subtitle') }}</p>
      </div>
      <button v-if="canManageProviders" @click="openCreateModal" class="flex items-center space-x-2 px-4 py-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">
        <Plus class="w-5 h-5" />
        <span class="font-mono text-sm truncate min-w-0">[{{ t('providers_add_btn') }}]</span>
      </button>
    </div>

    <div v-if="providers.length > 0" class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6">
      <div v-for="provider in providers" :key="provider.domain" class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl p-6 shadow-lg shadow-black/10 hover:border-indigo-500/20 transition-colors">
        <div class="flex flex-wrap items-center justify-between gap-2 mb-4">
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 min-w-0">
            <div class="p-2 rounded-xl bg-indigo-500/15 border border-indigo-500/20 shrink-0">
              <Bookmark class="w-5 h-5 text-indigo-300" />
            </div>
            <h3 class="text-xl font-bold text-white break-all min-w-0">{{ provider.name }}</h3>
          </div>
          <div class="flex space-x-2 shrink-0">
            <button v-if="canManageProviders" @click="openEditModal(provider)" class="text-blue-400 hover:text-blue-300 transition-colors" :title="t('providers_rename_tt')">
              <Edit class="w-5 h-5" />
            </button>
            <button v-if="canManageProviders" @click="deleteProvider(provider)" class="text-red-500 hover:text-red-700 transition-colors" :title="t('providers_delete_tt')">
              <Trash2 class="w-5 h-5" />
            </button>
          </div>
        </div>
        <code class="text-xs text-zinc-400 break-all font-mono">{{ provider.domain }}</code>
      </div>
    </div>

    <div v-else class="text-center py-16">
      <Bookmark class="w-16 h-16 text-zinc-600 mx-auto mb-4" />
      <p class="text-zinc-500 text-lg">{{ t('providers_empty') }}</p>
      <p class="text-zinc-600 text-sm mt-2">{{ t('providers_empty_hint') }}</p>
    </div>

    <div v-if="showModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ editing ? t('providers_edit_title') : t('providers_add_btn') }}<span class="font-mono text-indigo-400">]</span></h2>

        <form @submit.prevent="saveProvider">
          <div class="space-y-6">
            <div>
              <label class="block text-sm font-medium text-zinc-400 mb-1">{{ t('providers_domain') }}</label>
              <input v-model="form.domain" type="text" :disabled="editing" :placeholder="t('providers_domain_ph')"
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 disabled:opacity-50 disabled:cursor-not-allowed">
              <p class="mt-1 text-xs text-zinc-500">{{ t('providers_domain_hint') }}</p>
            </div>
            <div>
              <label class="block text-sm font-medium text-zinc-400 mb-1">{{ t('providers_name') }}</label>
              <input v-model="form.name" type="text" :placeholder="t('providers_name_ph')"
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="showModal = false"
                    class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
            <button type="submit"
                    class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">{{ editing ? t('save') : t('providers_add_btn') }}</button>
          </div>
        </form>
      </div>
    </div>

    <div v-if="toast" :class="['fixed bottom-20 md:bottom-6 right-4 md:right-6 z-50 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl border', toastType === 'success' ? 'bg-emerald-950/95 md:bg-emerald-500/15 border-emerald-500/40 text-emerald-200' : 'bg-red-950/95 md:bg-red-500/15 border-red-500/40 text-red-200']">
      {{ toast }}
    </div>
  </div>
</template>

<script>
import { ref, computed, inject, onMounted } from 'vue';
import { Plus, Trash2, Edit, Bookmark } from 'lucide-vue-next';

export default {
  name: 'Providers',
  components: { Plus, Trash2, Edit, Bookmark },
  setup() {
    const t = inject('t') || ((k) => k);
    const prefs = inject('prefs', ref({ theme_mode: 'obsidian' }));
    const authCtx = inject('authCtx', {});
    const modalBackdrop = computed(() => prefs.value.theme_mode === 'light' ? 'bg-zinc-900/70 backdrop-blur-md' : 'bg-black/70 backdrop-blur-md');
    const canManageProviders = computed(() => authCtx.canManageProviders?.value ?? false);

    const providers = ref([]);
    const showModal = ref(false);
    const editing = ref(false);
    const form = ref({ domain: '', name: '' });
    const toast = ref('');
    const toastType = ref('success');
    let toastTimer = null;

    const showToast = (msg, type = 'success', duration = 4000) => {
      toast.value = msg;
      toastType.value = type;
      clearTimeout(toastTimer);
      toastTimer = setTimeout(() => { toast.value = ''; }, duration);
    };

    const fetchProviders = async () => {
      try {
        const response = await fetch('/api/web/providers');
        if (response.ok) {
          providers.value = await response.json();
        } else {
          showToast(t('providers_fetch_failed'), 'error');
        }
      } catch (e) {
        console.error('Error fetching providers:', e);
        showToast(t('providers_fetch_failed'), 'error');
      }
    };

    const openCreateModal = () => {
      editing.value = false;
      form.value = { domain: '', name: '' };
      showModal.value = true;
    };

    const openEditModal = (provider) => {
      editing.value = true;
      form.value = { domain: provider.domain, name: provider.name };
      showModal.value = true;
    };

    const saveProvider = async () => {
      const domain = (form.value.domain || '').trim().toLowerCase();
      const name = (form.value.name || '').trim();
      if (!name) {
        showToast(t('providers_name_required'), 'error');
        return;
      }
      if (!domain || domain.includes('://') || /[/?#@]/.test(domain)) {
        showToast(t('providers_domain_required'), 'error');
        return;
      }
      try {
        const url = editing.value ? `/api/web/providers/${encodeURIComponent(domain)}` : '/api/web/providers';
        const method = editing.value ? 'PUT' : 'POST';
        const body = editing.value ? { name } : { domain, name };
        const response = await fetch(url, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!response.ok) {
          let errMsg = t('providers_save_failed');
          try {
            const errData = await response.json();
            if (errData.error) errMsg = errData.error;
          } catch (_) {}
          throw new Error(errMsg);
        }
        showModal.value = false;
        await fetchProviders();
        showToast(t('providers_saved'));
      } catch (e) {
        console.error('Error saving provider:', e);
        showToast(e.message || t('providers_save_failed'), 'error');
      }
    };

    const deleteProvider = async (provider) => {
      if (!confirm(t('providers_delete_confirm', { name: provider.name }))) return;
      try {
        const response = await fetch(`/api/web/providers/${encodeURIComponent(provider.domain)}`, { method: 'DELETE' });
        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || t('providers_delete_failed'));
        }
        await fetchProviders();
      } catch (e) {
        console.error('Error deleting provider:', e);
        showToast(e.message || t('providers_delete_failed'), 'error');
      }
    };

    onMounted(() => {
      fetchProviders();
    });

    return {
      providers,
      canManageProviders,
      showModal,
      editing,
      form,
      toast,
      toastType,
      modalBackdrop,
      openCreateModal,
      openEditModal,
      saveProvider,
      deleteProvider,
      t,
    };
  },
};
</script>