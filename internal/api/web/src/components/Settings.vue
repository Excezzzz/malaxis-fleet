<template>
  <div>
    <div class="flex justify-between items-center mb-8">
      <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>Settings<span class="font-mono text-indigo-400">]</span></h1>
    </div>

    <div class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl p-6 mb-6">
      <h2 class="text-2xl font-bold tracking-tight mb-6"><span class="font-mono text-indigo-400">[</span>Telegram Bot Settings<span class="font-mono text-indigo-400">]</span></h2>

      <div class="space-y-6">
        <div class="flex items-center justify-between">
          <div>
            <p class="text-sm font-medium text-zinc-300">Telegram Bot</p>
            <p class="text-xs text-zinc-500">{{ botEnabled ? 'Bot is active' : 'Bot is disabled' }}</p>
          </div>
          <button @click="botEnabled = !botEnabled" class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors" :class="botEnabled ? 'bg-indigo-500' : 'bg-zinc-700'">
            <span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform" :class="botEnabled ? 'translate-x-6' : 'translate-x-1'"></span>
          </button>
        </div>

        <div>
          <label class="block text-sm font-medium text-zinc-400 mb-1">Bot Token</label>
          <input v-model="botToken" type="password" placeholder="123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
                 class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 font-mono text-sm">
        </div>

        <div>
          <label class="block text-sm font-medium text-zinc-400 mb-1">Admin Chat ID</label>
          <input v-model="adminChatId" type="number" placeholder="987654321"
                 class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
        </div>

        <div class="flex space-x-4">
          <button @click="saveBotSettings" :disabled="saving" class="px-4 py-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors disabled:opacity-50">
            <span class="font-mono text-sm" v-if="saving">[Saving...]</span>
            <span class="font-mono text-sm" v-else>[Save Bot Settings]</span>
          </button>
          <button @click="testConnection" :disabled="testing" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 text-zinc-200 rounded-xl transition-colors disabled:opacity-50">
            <span class="font-mono text-sm" v-if="testing">[Testing...]</span>
            <span class="font-mono text-sm" v-else>[Test Connection]</span>
          </button>
          <button @click="restoreAvatar" :disabled="avatarResetting" class="px-4 py-2 bg-indigo-600/15 hover:bg-indigo-600/25 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors disabled:opacity-50">
            <span class="font-mono text-sm" v-if="avatarResetting">[Restoring...]</span>
            <span class="font-mono text-sm" v-else>[🎨 Restore Bot Avatar]</span>
          </button>
        </div>

        <div v-if="testResult" class="text-sm" :class="testResult.success ? 'text-green-400' : 'text-red-400'">
          <p v-if="testResult.success">Connected as @{{ testResult.bot_name }} (ID: {{ testResult.bot_id }})</p>
          <p v-else>Connection failed: {{ testResult.error }}</p>
        </div>

        <div v-if="saveMessage" class="text-sm" :class="saveMessage.type === 'success' ? 'text-green-400' : 'text-red-400'">
          {{ saveMessage.text }}
        </div>
      </div>
    </div>

    <div v-if="avatarToast" class="fixed bottom-20 md:bottom-6 right-4 md:right-6 z-50 bg-emerald-500/15 border border-emerald-500/40 text-emerald-200 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl">
      {{ avatarToast }}
    </div>

    <div class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl p-6">
      <h2 class="text-2xl font-bold tracking-tight mb-6"><span class="font-mono text-indigo-400">[</span>Database Backup<span class="font-mono text-indigo-400">]</span></h2>
      <p class="text-zinc-400 mb-4">Download a complete database backup as a zip archive.</p>
      <button @click="downloadBackup" class="px-4 py-2 bg-green-500/15 hover:bg-green-500/25 border border-green-500/30 text-green-100 rounded-xl transition-colors">
        <span class="font-mono text-sm">[Download Backup]</span>
      </button>
    </div>
  </div>
</template>

<script>
import { ref, onMounted } from 'vue';

export default {
  name: 'Settings',
  setup() {
    const botEnabled = ref(false);
    const botToken = ref('');
    const adminChatId = ref('');
    const saving = ref(false);
    const testing = ref(false);
    const avatarResetting = ref(false);
    const testResult = ref(null);
    const saveMessage = ref(null);
    const avatarToast = ref('');
    let avatarToastTimer = null;

    const fetchSettings = async () => {
      try {
        const resp = await fetch('/api/web/settings');
        if (resp.ok) {
          const data = await resp.json();
          botEnabled.value = data.tg_bot_enabled || false;
          botToken.value = data.tg_bot_token || '';
          adminChatId.value = data.tg_admin_chat_id || '';
        }
      } catch (e) {
        console.error('Failed to fetch settings:', e);
      }
    };

    const saveBotSettings = async () => {
      saving.value = true;
      saveMessage.value = null;
      try {
        const resp = await fetch('/api/web/settings/bot', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            enabled: botEnabled.value,
            token: botToken.value,
            chat_id: parseInt(adminChatId.value) || 0,
          }),
        });
        if (resp.ok) {
          saveMessage.value = { type: 'success', text: 'Bot settings saved. Bot is rebooting with new settings...' };
        } else {
          const err = await resp.text();
          saveMessage.value = { type: 'error', text: 'Failed to save: ' + err };
        }
      } catch (e) {
        saveMessage.value = { type: 'error', text: 'Error: ' + e.message };
      } finally {
        saving.value = false;
      }
    };

    const testConnection = async () => {
      if (!botToken.value) {
        testResult.value = { success: false, error: 'Enter a bot token first' };
        return;
      }
      testing.value = true;
      testResult.value = null;
      try {
        const resp = await fetch('/api/web/settings/bot/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ token: botToken.value }),
        });
        const data = await resp.json();
        testResult.value = data;
      } catch (e) {
        testResult.value = { success: false, error: e.message };
      } finally {
        testing.value = false;
      }
    };

    const restoreAvatar = async () => {
      avatarResetting.value = true;
      try {
        const resp = await fetch('/api/web/settings/bot/reset-avatar', { method: 'POST' });
        if (!resp.ok) throw new Error((await resp.text()) || 'Failed to restore avatar');
        avatarToast.value = 'Bot profile photo updated!';
      } catch (e) {
        avatarToast.value = 'Could not restore avatar: ' + e.message;
      } finally {
        avatarResetting.value = false;
        clearTimeout(avatarToastTimer);
        avatarToastTimer = setTimeout(() => { avatarToast.value = ''; }, 4000);
      }
    };

    const downloadBackup = async () => {
      try {
        const resp = await fetch('/api/web/backup/download');
        if (!resp.ok) throw new Error('Failed to create backup');
        const blob = await resp.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'malaxis_fleet_backup.zip';
        a.click();
        URL.revokeObjectURL(url);
      } catch (e) {
        console.error('Backup download failed:', e);
        alert('Failed to download backup: ' + e.message);
      }
    };

    onMounted(() => {
      fetchSettings();
    });

    return {
      botEnabled, botToken, adminChatId, saving, testing, avatarResetting, testResult, saveMessage, avatarToast,
      saveBotSettings, testConnection, restoreAvatar, downloadBackup,
    };
  },
};
</script>
