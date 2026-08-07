<template>
  <div>
    <div class="flex justify-between items-center mb-8">
      <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('settings_title') }}<span class="font-mono text-indigo-400">]</span></h1>
    </div>

    <div class="max-w-full overflow-hidden p-4 sm:p-6 bg-zinc-900/60 backdrop-blur-xl border border-white/10 rounded-2xl mb-6">
      <h2 class="text-2xl font-bold tracking-tight mb-6"><span class="font-mono text-indigo-400">[</span>{{ t('settings_appearance') }}<span class="font-mono text-indigo-400">]</span></h2>

      <div class="space-y-6">
        <div>
          <p class="text-sm font-medium text-zinc-300 mb-3">{{ t('settings_accent') }}</p>
          <div class="flex flex-wrap items-center gap-3">
            <button v-for="(hex, id) in ACCENTS" :key="id" @click="setAccent(id)"
              class="w-8 h-8 rounded-full border-2 transition-all cursor-pointer hover:scale-110"
              :class="prefs.accent_color === id ? 'border-white ring-2 ring-indigo-400/50' : 'border-white/20'"
              :style="{ backgroundColor: hex }" :title="id"></button>
          </div>
        </div>

        <div>
          <p class="text-sm font-medium text-zinc-300 mb-3">{{ t('settings_theme') }}</p>
          <div class="flex items-center gap-2">
            <button @click="setTheme('obsidian')"
              :class="['inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all cursor-pointer border', prefs.theme_mode === 'obsidian' ? 'bg-white/10 border-white/30 text-white' : 'bg-white/5 border-white/10 text-zinc-400 hover:text-white hover:bg-white/10']">🖤 Obsidian</button>
            <button @click="setTheme('dark')"
              :class="['inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all cursor-pointer border', prefs.theme_mode === 'dark' ? 'bg-white/10 border-white/30 text-white' : 'bg-white/5 border-white/10 text-zinc-400 hover:text-white hover:bg-white/10']">🌙 Dark</button>
            <button @click="setTheme('light')"
              :class="['inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all cursor-pointer border', prefs.theme_mode === 'light' ? 'bg-white/10 border-white/30 text-white' : 'bg-white/5 border-white/10 text-zinc-400 hover:text-white hover:bg-white/10']">☀️ Light</button>
          </div>
        </div>

        <div>
          <p class="text-sm font-medium text-zinc-300 mb-3">{{ t('settings_language') }}</p>
          <div class="flex items-center gap-2">
            <button @click="setLanguage('ru')"
              :class="['inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all cursor-pointer border', prefs.language === 'ru' ? 'bg-white/10 border-white/30 text-white' : 'bg-white/5 border-white/10 text-zinc-400 hover:text-white hover:bg-white/10']">[RU]</button>
            <button @click="setLanguage('en')"
              :class="['inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all cursor-pointer border', prefs.language === 'en' ? 'bg-white/10 border-white/30 text-white' : 'bg-white/5 border-white/10 text-zinc-400 hover:text-white hover:bg-white/10']">[EN]</button>
          </div>
        </div>
      </div>
    </div>

    <div class="max-w-full overflow-hidden p-4 sm:p-6 bg-zinc-900/60 backdrop-blur-xl border border-white/10 rounded-2xl mb-6">
      <h2 class="text-2xl font-bold tracking-tight mb-2"><span class="font-mono text-indigo-400">[</span>{{ t('settings_auto_backups') }}<span class="font-mono text-indigo-400">]</span></h2>
      <p class="text-zinc-400 mb-6 text-sm">{{ t('settings_auto_backups_hint') }}</p>

      <div class="space-y-4">
        <div class="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 py-3 border-b border-white/5">
          <div>
            <p class="text-sm font-medium text-zinc-300">{{ t('settings_backup_local') }}</p>
            <p class="text-xs text-zinc-500">{{ t('settings_backup_local_desc') }}</p>
          </div>
          <button @click="backupToLocal = !backupToLocal" class="relative inline-flex h-6 w-11 shrink-0 max-w-full items-center rounded-full transition-colors" :class="backupToLocal ? 'bg-indigo-500' : 'bg-zinc-500'">
            <span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform" :class="backupToLocal ? 'translate-x-6' : 'translate-x-1'"></span>
          </button>
        </div>

        <div class="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 py-3 border-b border-white/5">
          <div>
            <p class="text-sm font-medium text-zinc-300">{{ t('settings_backup_tg') }}</p>
            <p class="text-xs text-zinc-500">{{ t('settings_backup_tg_desc') }}</p>
          </div>
          <button @click="backupToTelegram = !backupToTelegram" class="relative inline-flex h-6 w-11 shrink-0 max-w-full items-center rounded-full transition-colors" :class="backupToTelegram ? 'bg-indigo-500' : 'bg-zinc-500'">
            <span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform" :class="backupToTelegram ? 'translate-x-6' : 'translate-x-1'"></span>
          </button>
        </div>

        <div class="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 py-3 border-b border-white/5">
          <div>
            <p class="text-sm font-medium text-zinc-300">{{ t('settings_backup_interval') }}</p>
            <p class="text-xs text-zinc-500">{{ t('settings_backup_interval_desc') }}</p>
          </div>
          <select v-model="backupIntervalHours" class="w-auto shrink-0 max-w-full bg-zinc-900 border border-white/10 rounded-xl px-3 py-2 text-sm text-zinc-300 focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 transition-colors cursor-pointer">
            <option :value="6">{{ t('settings_backup_interval_6') }}</option>
            <option :value="12">{{ t('settings_backup_interval_12') }}</option>
            <option :value="24">{{ t('settings_backup_interval_24') }}</option>
            <option :value="168">{{ t('settings_backup_interval_168') }}</option>
          </select>
        </div>

        <div class="flex space-x-4">
          <button @click="saveBackupSettings" :disabled="savingBackup" class="inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all bg-indigo-600 hover:bg-indigo-500 text-white shadow-md disabled:opacity-50">
            <span class="font-mono text-sm truncate min-w-0">[{{ t('save') }}]</span>
          </button>
        </div>

        <div v-if="backupMessage" class="text-sm" :class="backupMessage.type === 'success' ? 'text-green-400' : 'text-red-400'">
          {{ backupMessage.text }}
        </div>
      </div>
    </div>

    <div class="max-w-full overflow-hidden p-4 sm:p-6 bg-zinc-900/60 backdrop-blur-xl border border-white/10 rounded-2xl mb-6">
      <h2 class="text-2xl font-bold tracking-tight mb-6"><span class="font-mono text-indigo-400">[</span>{{ t('settings_bot') }}<span class="font-mono text-indigo-400">]</span></h2>

      <div class="space-y-6">
        <div class="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 py-3 border-b border-white/5">
          <div>
            <p class="text-sm font-medium text-zinc-300">{{ t('settings_telegram_bot') }}</p>
            <p class="text-xs text-zinc-500">{{ botEnabled ? t('settings_bot_active') : t('settings_bot_disabled') }}</p>
          </div>
          <button @click="botEnabled = !botEnabled" class="relative inline-flex h-6 w-11 shrink-0 max-w-full items-center rounded-full transition-colors" :class="botEnabled ? 'bg-indigo-500' : 'bg-zinc-500'">
            <span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform" :class="botEnabled ? 'translate-x-6' : 'translate-x-1'"></span>
          </button>
        </div>

        <div>
          <label class="block text-sm font-medium text-zinc-400 mb-1">{{ t('settings_bot_token') }}</label>
          <input v-model="botToken" type="password" placeholder="123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
                 class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 font-mono text-sm">
        </div>

        <div>
          <label class="block text-sm font-medium text-zinc-400 mb-1">{{ t('settings_admin_chat') }}</label>
          <input v-model="adminChatId" type="number" placeholder="987654321"
                 class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
        </div>

        <div class="flex space-x-4">
          <button @click="saveBotSettings" :disabled="saving" class="inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all bg-indigo-600 hover:bg-indigo-500 text-white shadow-md disabled:opacity-50">
            <span class="font-mono text-sm" v-if="saving">[{{ t('settings_saving_bot') }}]</span>
            <span class="font-mono text-sm" v-else>[{{ t('settings_save_bot') }}]</span>
          </button>
          <button @click="testConnection" :disabled="testing" class="inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all bg-zinc-100 hover:bg-zinc-200 text-zinc-900 border border-zinc-300 disabled:opacity-50">
            <span class="font-mono text-sm" v-if="testing">[{{ t('settings_testing') }}]</span>
            <span class="font-mono text-sm" v-else>[{{ t('settings_test') }}]</span>
          </button>
        </div>

        <div v-if="testResult" class="text-sm" :class="testResult.success ? 'text-green-400' : 'text-red-400'">
          <p v-if="testResult.success">{{ t('settings_test_ok') }} @{{ testResult.bot_name }} (ID: {{ testResult.bot_id }})</p>
          <p v-else>{{ t('settings_test_fail') }}: {{ testResult.error }}</p>
        </div>

        <div v-if="saveMessage" class="text-sm" :class="saveMessage.type === 'success' ? 'text-green-400' : 'text-red-400'">
          {{ saveMessage.text }}
        </div>

        <div class="pt-4 border-t border-white/5">
          <p class="text-sm font-medium text-zinc-300 mb-1">{{ t('settings_avatar_color') }}</p>
          <p class="text-xs text-zinc-500 mb-3">{{ t('settings_avatar_color_hint') }}</p>
          <div class="flex flex-wrap items-center gap-3">
            <button v-for="(hex, id) in ACCENTS" :key="id" @click="botAvatarColor = id"
              class="w-8 h-8 rounded-full border-2 transition-all cursor-pointer hover:scale-110"
              :class="botAvatarColor === id ? 'border-white ring-2 ring-indigo-400/50' : 'border-white/20'"
              :style="{ backgroundColor: hex }" :title="t('color_' + id)"></button>
          </div>
          <div class="mt-4 flex flex-wrap items-center gap-3">
            <button @click="applyBotAvatar" :disabled="avatarApplying"
              class="inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all bg-indigo-600 hover:bg-indigo-500 text-white shadow-md disabled:opacity-50">
              <span class="font-mono text-sm" v-if="avatarApplying">[{{ t('settings_avatar_applying') }}]</span>
              <span class="font-mono text-sm" v-else>[{{ t('settings_avatar_apply') }}]</span>
            </button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="avatarToast" class="fixed bottom-20 md:bottom-6 right-4 md:right-6 z-50 bg-emerald-500/15 border border-emerald-500/40 text-emerald-200 px-5 py-3 rounded-xl backdrop-blur-md shadow-2xl">
      {{ avatarToast }}
    </div>

    <div class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl p-6">
      <h2 class="text-2xl font-bold tracking-tight mb-6"><span class="font-mono text-indigo-400">[</span>{{ t('settings_backup') }}<span class="font-mono text-indigo-400">]</span></h2>
      <p class="text-zinc-400 mb-4">{{ t('settings_backup_hint') }}</p>
      <button @click="downloadBackup" class="inline-flex items-center justify-center py-2.5 px-4 text-xs font-semibold rounded-xl transition-all bg-indigo-600 hover:bg-indigo-500 text-white shadow-md">
        <span class="font-mono text-sm truncate min-w-0">[{{ t('settings_backup_btn') }}]</span>
      </button>
    </div>
  </div>
</template>

<script>
import { ref, onMounted, inject } from 'vue';

const ACCENTS = {
  indigo: '#6366f1',
  emerald: '#10b981',
  amber: '#f59e0b',
  rose: '#f43f5e',
  cyan: '#06b6d4',
};

export default {
  name: 'Settings',
  setup() {
    const t = inject('t') || ((k) => k);
    const prefs = inject('prefs', ref({ accent_color: 'indigo', theme_mode: 'obsidian', language: 'ru' }));
    const savePrefs = inject('savePrefs', null);
    const botEnabled = ref(false);
    const botToken = ref('');
    const adminChatId = ref('');
    const saving = ref(false);
    const testing = ref(false);
    const botAvatarColor = ref('indigo');
    const avatarApplying = ref(false);
    const testResult = ref(null);
    const saveMessage = ref(null);
    const avatarToast = ref('');
    const backupToLocal = ref(true);
    const backupToTelegram = ref(false);
    const backupIntervalHours = ref(24);
    const savingBackup = ref(false);
    const backupMessage = ref(null);
    let avatarToastTimer = null;

    const setAccent = async (id) => {
      if (savePrefs) {
        await savePrefs({ accent_color: id });
      } else {
        prefs.value = { ...prefs.value, accent_color: id };
      }
    };

    const setTheme = async (id) => {
      if (savePrefs) {
        await savePrefs({ theme_mode: id });
      } else {
        prefs.value = { ...prefs.value, theme_mode: id };
      }
    };

    const setLanguage = async (lang) => {
      if (savePrefs) {
        await savePrefs({ language: lang });
      } else {
        prefs.value = { ...prefs.value, language: lang };
      }
    };

    const fetchSettings = async () => {
      try {
        const resp = await fetch('/api/web/settings');
        if (resp.ok) {
          const data = await resp.json();
          botEnabled.value = data.tg_bot_enabled || false;
          botToken.value = data.tg_bot_token || '';
          adminChatId.value = data.tg_admin_chat_id || '';
          backupToLocal.value = data.backup_to_local !== false;
          backupToTelegram.value = data.backup_to_telegram === true;
          backupIntervalHours.value = data.backup_interval_hours || 24;
          botAvatarColor.value = data.bot_avatar_color || 'indigo';
        }
      } catch (e) {
        console.error('Failed to fetch settings:', e);
      }
    };

    const saveBackupSettings = async () => {
      savingBackup.value = true;
      backupMessage.value = null;
      try {
        const resp = await fetch('/api/web/settings/backup', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            backup_to_local: backupToLocal.value,
            backup_to_telegram: backupToTelegram.value,
            backup_interval_hours: parseInt(backupIntervalHours.value) || 24,
          }),
        });
        if (resp.ok) {
          backupMessage.value = { type: 'success', text: t('settings_bot_saved') };
        } else {
          const err = await resp.text();
          backupMessage.value = { type: 'error', text: t('settings_save_failed', { err }) };
        }
      } catch (e) {
        backupMessage.value = { type: 'error', text: t('settings_error', { err: e.message }) };
      } finally {
        savingBackup.value = false;
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
          saveMessage.value = { type: 'success', text: t('settings_bot_saved') };
        } else {
          const err = await resp.text();
          saveMessage.value = { type: 'error', text: t('settings_save_failed', { err }) };
        }
      } catch (e) {
        saveMessage.value = { type: 'error', text: t('settings_error', { err: e.message }) };
      } finally {
        saving.value = false;
      }
    };

    const testConnection = async () => {
      if (!botToken.value) {
        testResult.value = { success: false, error: t('settings_test_no_token') };
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

    const applyBotAvatar = async () => {
      avatarApplying.value = true;
      try {
        const resp = await fetch('/api/web/settings/bot/avatar', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ color: botAvatarColor.value }),
        });
        if (!resp.ok) throw new Error((await resp.text()) || 'Failed to apply avatar');
        avatarToast.value = t('settings_avatar_ok');
      } catch (e) {
        avatarToast.value = t('settings_avatar_fail') + ': ' + e.message;
      } finally {
        avatarApplying.value = false;
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
        alert(t('settings_backup_download_failed', { err: e.message }));
      }
    };

    onMounted(() => {
      fetchSettings();
    });

    return {
      botEnabled, botToken, adminChatId, saving, testing, avatarApplying, botAvatarColor, testResult, saveMessage, avatarToast,
      saveBotSettings, testConnection, applyBotAvatar, downloadBackup,
      ACCENTS, prefs, setAccent, setTheme, setLanguage,
      backupToLocal, backupToTelegram, backupIntervalHours, savingBackup, backupMessage, saveBackupSettings,
      t,
    };
  },
};
</script>
