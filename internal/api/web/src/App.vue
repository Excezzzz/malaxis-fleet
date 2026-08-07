<template>
  <div class="bg-zinc-950 text-white min-h-screen font-sans relative">
    <div v-if="!isAuthenticated">
      <Login @authenticated="login" />
    </div>
    <div v-else class="relative min-h-screen">
      <!-- Mobile top bar: logo / user badge / logout -->
      <header class="fixed top-0 left-0 right-0 z-50 md:hidden flex items-center justify-between gap-3 px-4 py-2.5 bg-zinc-900/60 backdrop-blur-xl border-b border-white/10">
        <div class="flex items-center space-x-2 shrink-0 min-w-0">
          <div class="p-1.5 rounded-lg bg-indigo-500/15 border border-indigo-400/20 shrink-0">
            <Globe class="w-4 h-4 text-indigo-300" />
          </div>
          <h1 class="text-sm font-bold tracking-tight whitespace-nowrap truncate">Malaxis Fleet</h1>
        </div>
        <div class="flex items-center space-x-2 shrink-0">
          <div v-if="username" class="flex flex-col text-right min-w-0">
            <span class="text-xs font-bold text-white whitespace-nowrap truncate">{{ username }}</span>
            <span v-if="roleName" :class="['text-[10px] uppercase tracking-wider truncate', roleName.toLowerCase() === 'owner' ? 'text-red-400' : 'text-indigo-400']">{{ roleName }}</span>
            <span v-else class="text-[10px] uppercase tracking-wider truncate text-zinc-500">user</span>
          </div>
          <button @click="logout" class="flex items-center justify-center w-9 h-9 rounded-full text-red-400 hover:text-red-300 hover:bg-red-950/30 transition-colors" :title="t('logout')">
            <LogOut class="w-4 h-4" />
          </button>
        </div>
      </header>

      <!-- Mobile bottom island nav: floating capsule, icons only -->
      <nav class="fixed bottom-4 left-4 right-4 z-50 md:hidden bg-zinc-900/80 backdrop-blur-xl border border-white/10 rounded-full shadow-2xl px-4 py-2.5 flex justify-around items-center">
        <a v-for="item in navItems" :key="item.view" @click.prevent="currentView = item.view" href="#"
           :title="item.label"
           :class="['relative flex items-center justify-center w-10 h-10 rounded-full transition-colors', currentView === item.view ? 'text-indigo-400' : 'text-zinc-500 hover:text-zinc-300']">
          <component :is="item.icon" class="w-5 h-5" />
          <span v-if="currentView === item.view"
            class="absolute -bottom-0.5 left-1/2 -translate-x-1/2 w-1 h-1 rounded-full bg-indigo-400 shadow-[0_0_8px_2px_rgba(129,140,248,0.6)]"></span>
        </a>
      </nav>

      <nav class="hidden md:flex fixed top-4 left-4 right-4 z-50 max-w-[1600px] mx-auto items-center gap-2 sm:gap-3 px-4 sm:px-6 py-2.5 sm:py-4 bg-zinc-900/80 backdrop-blur-xl border border-white/10 rounded-full shadow-2xl shadow-black/40">
        <div class="flex items-center space-x-2 sm:space-x-3 shrink-0">
          <div class="p-1.5 sm:p-2 rounded-xl bg-indigo-500/15 border border-indigo-400/20">
            <Globe class="w-4 h-4 sm:w-5 sm:h-5 text-indigo-300" />
          </div>
          <div class="leading-tight">
            <h1 class="text-sm sm:text-lg font-bold tracking-tight whitespace-nowrap">Malaxis Fleet</h1>
            <span class="hidden sm:inline-block bg-indigo-600/90 text-white font-bold px-2.5 py-0.5 text-xs rounded-md tracking-wider shadow-lg shadow-indigo-950/50">v1.0.0</span>
          </div>
        </div>

        <div class="flex-1 min-w-0 flex items-center gap-1 overflow-x-auto whitespace-nowrap scrollbar-none md:justify-center">
          <a v-for="item in navItems" :key="item.view" @click.prevent="currentView = item.view" href="#"
             :title="item.label"
             class="flex items-center space-x-2 px-2.5 sm:px-3 lg:px-4 py-2 rounded-full text-sm transition-colors whitespace-nowrap shrink-0"
             :class="navLinkClasses(item.view)">
            <component :is="item.icon" class="w-4 h-4" />
            <span class="hidden lg:inline font-mono text-xs">[{{ item.label }}]</span>
          </a>
        </div>

        <div class="flex items-center space-x-2 sm:space-x-3 shrink-0">
          <div v-if="username" class="hidden sm:flex flex-col text-right mr-2">
            <span class="text-sm font-bold text-white whitespace-nowrap">{{ username }}</span>
            <span v-if="roleName" class="text-[10px] uppercase tracking-wider text-indigo-400">{{ roleName }}</span>
          </div>
          <button @click="logout" class="flex items-center space-x-1.5 px-2.5 sm:px-3 py-2 rounded-full text-sm text-red-400 hover:text-red-300 hover:bg-red-950/30 transition-colors">
            <LogOut class="w-4 h-4" />
            <span class="hidden min-[420px]:inline">{{ t('logout') }}</span>
          </button>
        </div>
      </nav>

      <main class="w-full px-4 md:px-8 pt-16 md:pt-28 pb-24 md:pb-16">
        <div v-if="isReadOnly" class="mb-6 px-4 py-3 rounded-xl border border-amber-500/30 bg-amber-500/10 text-amber-200 text-sm">
          <strong>{{ t('readonly_banner') }}</strong>
        </div>
        <component :is="currentViewComponent" />
      </main>
    </div>
  </div>
</template>

<script>
import { ref, computed, onMounted, provide } from 'vue';
import { Globe, Server, Users, Shield, LogOut, FileCode, Terminal, Settings } from 'lucide-vue-next';
import Login from './components/Login.vue';
import Nodes from './components/Nodes.vue';
import ClientFiles from './components/ClientFiles.vue';
import AdminUsers from './components/AdminUsers.vue';
import RoleManager from './components/RoleManager.vue';
import AuditLogs from './components/AuditLogs.vue';
import SettingsView from './components/Settings.vue';
import messages from './i18n';

const ACCENTS = {
  indigo: '#6366f1',
  emerald: '#10b981',
  amber: '#f59e0b',
  rose: '#f43f5e',
  cyan: '#06b6d4',
};

const THEMES = {
  obsidian: { base: '#000000', surface: '#09090b' },
  dark: { base: '#09090b', surface: '#18181b' },
  light: { base: '#f4f4f5', surface: '#ffffff' },
};

const mix = (hex, target, amt) => {
  const n = parseInt(hex.slice(1), 16);
  const tn = parseInt(target.slice(1), 16);
  const mixCh = (sh) => Math.round(((n >> sh) & 255) * (1 - amt) + ((tn >> sh) & 255) * amt);
  return `rgb(${mixCh(16)},${mixCh(8)},${mixCh(0)})`;
};

export default {
  name: 'App',
  components: {
    Globe, Server, Users, Shield, LogOut, FileCode, Terminal, Settings,
    Login,
    Nodes,
    ClientFiles,
    AdminUsers,
    RoleManager,
    AuditLogs,
    SettingsView,
  },
  setup() {
    const isAuthenticated = ref(false);
    const currentView = ref('Nodes');
    const role = ref('');
    const roleName = ref('');
    const username = ref('');
    const permissions = ref([]);
    const prefs = ref({ accent_color: 'indigo', theme_mode: 'obsidian', language: 'ru', bot_emojis_enabled: true });

    const applyPrefs = () => {
      const root = document.documentElement;
      const acc = ACCENTS[prefs.value.accent_color] || ACCENTS.indigo;
      const theme = THEMES[prefs.value.theme_mode] || THEMES.obsidian;
      const light = prefs.value.theme_mode === 'light';

      root.style.setProperty('--acc', acc);
      root.style.setProperty('--acc-text', light ? mix(acc, '#000000', 0.18) : mix(acc, '#ffffff', 0.58));
      root.style.setProperty('--acc-text-2', light ? mix(acc, '#000000', 0.12) : mix(acc, '#ffffff', 0.42));
      root.style.setProperty('--acc-dark', mix(acc, '#000000', 0.45));
      root.style.setProperty('--bg-base', theme.base);
      root.style.setProperty('--bg-surface', theme.surface);
      root.style.setProperty('--text-main', light ? '#18181b' : '#ffffff');
      root.style.setProperty('--text-2', light ? '#27272a' : '#f4f4f5');
      root.style.setProperty('--text-3', light ? '#3f3f46' : '#d4d4d8');
      root.style.setProperty('--text-4', light ? '#52525b' : '#a1a1aa');
      root.style.setProperty('--text-5', light ? '#71717a' : '#71717a');
      root.style.setProperty('--border-soft', light ? 'rgba(0,0,0,0.12)' : 'rgba(255,255,255,0.1)');
      root.style.setProperty('--border-softer', light ? 'rgba(0,0,0,0.06)' : 'rgba(255,255,255,0.05)');
      root.style.setProperty('--bg-input', light ? '#ffffff' : '#27272a');
      root.style.setProperty('--bg-input-2', light ? '#ffffff' : '#3f3f46');
      root.style.setProperty('--danger', light ? '#b91c1c' : '#f87171');
      root.style.setProperty('--danger-strong', light ? '#991b1b' : '#fca5a5');
      root.style.setProperty('--success', light ? '#15803d' : '#4ade80');
      root.style.setProperty('--success-strong', light ? '#166534' : '#86efac');
      root.style.setProperty('--warning', light ? '#b45309' : '#fbbf24');
      root.style.setProperty('--warning-strong', light ? '#92400e' : '#fde68a');
      root.style.setProperty('--info', light ? '#0369a1' : '#38bdf8');
      document.body.style.backgroundColor = theme.base;
      localStorage.setItem('fleet_prefs', JSON.stringify(prefs.value));
    };

    const savePrefs = async (patch) => {
      prefs.value = { ...prefs.value, ...patch };
      applyPrefs();
      try {
        await fetch('/api/web/user/preferences', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(prefs.value),
        });
      } catch (e) {
        console.error('Failed to save preferences:', e);
      }
    };

    const setAccent = (id) => savePrefs({ accent_color: id });
    const setTheme = (id) => savePrefs({ theme_mode: id });
    const setLanguage = (lang) => savePrefs({ language: lang });

    const t = (key, vars) => {
      let s = (messages[prefs.value.language] && messages[prefs.value.language][key]) || key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) s = s.replaceAll('{' + k + '}', String(v));
      }
      return s;
    };

    provide('t', t);
    provide('prefs', prefs);
    provide('savePrefs', savePrefs);

    const hasPermission = (perm) => {
      if (role.value === 'owner' || username.value === 'admin') return true;
      return permissions.value.includes(perm);
    };

    const canViewNodes = computed(() => hasPermission('can_view_nodes'));
    const canEditSub = computed(() => hasPermission('can_edit_sub'));
    const canSwitchVpn = computed(() => hasPermission('can_switch_vpn'));
    const canRenameNode = computed(() => hasPermission('can_rename_node'));
    const canTerminateNode = computed(() => hasPermission('can_terminate_node'));
    const canPurgeNodes = computed(() => hasPermission('can_purge_nodes'));
    const canUpdateClient = computed(() => hasPermission('can_update_client'));
    const canViewNodeLogs = computed(() => hasPermission('can_view_node_logs'));
    const canViewUsers = computed(() => hasPermission('can_view_users'));
    const canCreateUsers = computed(() => hasPermission('can_create_users'));
    const canEditUsers = computed(() => hasPermission('can_edit_users'));
    const canDeleteUsers = computed(() => hasPermission('can_delete_users'));
    const canViewRoles = computed(() => hasPermission('can_view_roles'));
    const canManageRoles = computed(() => hasPermission('can_manage_roles'));
    const canViewAuditLogs = computed(() => hasPermission('can_view_audit_logs'));
    const canViewMasterLogs = computed(() => hasPermission('can_view_master_logs'));
    const isOwner = computed(() => role.value === 'owner' || username.value === 'admin');
    const isReadOnly = computed(() => !canEditSub.value && !canSwitchVpn.value && !canRenameNode.value && !canTerminateNode.value && !canPurgeNodes.value && !canUpdateClient.value && !canViewNodeLogs.value);

    const navItems = computed(() => {
      const items = [];
      if (canViewNodes.value) items.push({ view: 'Nodes', label: t('nav_nodes'), icon: 'Server' });
      if (canEditSub.value || canUpdateClient.value) items.push({ view: 'ClientFiles', label: t('nav_client_files'), icon: 'FileCode' });
      if (canViewUsers.value) items.push({ view: 'AdminUsers', label: t('nav_users'), icon: 'Users' });
      if (canViewRoles.value) items.push({ view: 'RoleManager', label: t('nav_roles'), icon: 'Shield' });
      if (canViewAuditLogs.value || canViewMasterLogs.value) items.push({ view: 'AuditLogs', label: t('nav_logs'), icon: 'Terminal' });
      if (isOwner.value) items.push({ view: 'Settings', label: t('nav_settings'), icon: 'Settings' });
      return items;
    });

    const currentViewComponent = computed(() => {
      switch (currentView.value) {
        case 'Nodes': return 'Nodes';
        case 'ClientFiles': return 'ClientFiles';
        case 'AdminUsers': return 'AdminUsers';
        case 'RoleManager': return 'RoleManager';
        case 'AuditLogs': return 'AuditLogs';
        case 'Settings': return 'SettingsView';
        default: return 'Nodes';
      }
    });

    const navLinkClasses = (viewName) => {
        return currentView.value === viewName
            ? 'bg-white/10 text-white font-semibold shadow-inner'
            : 'text-zinc-400 hover:bg-white/5 hover:text-white transition-colors';
    };

    const applyUser = (user) => {
        role.value = user.role || '';
        roleName.value = user.role_name || user.role || '';
        username.value = user.username || '';
        permissions.value = Array.isArray(user.permissions) ? user.permissions : [];
    };

    const fetchPreferences = async () => {
      try {
        const resp = await fetch('/api/web/user/preferences');
        if (resp.ok) {
          prefs.value = { ...prefs.value, ...(await resp.json()) };
          applyPrefs();
        }
      } catch (e) {
        console.error('Failed to fetch preferences:', e);
      }
    };

    const login = () => {
        isAuthenticated.value = true;
        fetch('/api/auth/me')
            .then(r => {
                if (!r.ok) throw new Error('Session not established');
                return r.json();
            })
            .then(user => applyUser(user))
            .catch(e => {
                console.error('Failed to fetch current user:', e);
                isAuthenticated.value = false;
            });
        fetchPreferences();
    };

    const checkAuth = () => {
        fetch('/api/auth/me')
            .then(r => {
                if (r.ok) return r.json();
                throw new Error('Not authenticated');
            })
            .then(user => {
                isAuthenticated.value = true;
                applyUser(user);
                fetchPreferences();
            })
            .catch(() => {
                isAuthenticated.value = false;
            });
    };

    onMounted(() => {
        const cached = localStorage.getItem('fleet_prefs');
        if (cached) {
          try {
            prefs.value = { ...prefs.value, ...JSON.parse(cached) };
          } catch (e) { /* ignore corrupt cache */ }
        }
        applyPrefs();
        checkAuth();
    });

    const logout = () => {
        isAuthenticated.value = false;
        role.value = '';
        permissions.value = [];
        fetch('/api/auth/logout', { method: 'POST' }).catch(() => {});
    };

        const user = computed(() => ({
      username: username.value,
      role: role.value,
    }));

    provide('authCtx', {
      user, hasPermission, canEditSub, canSwitchVpn, canViewNodes, isReadOnly,
      canRenameNode, canTerminateNode, canPurgeNodes, canUpdateClient,
      canViewNodeLogs, canViewAuditLogs, canViewMasterLogs,
      canViewUsers, canCreateUsers, canEditUsers, canDeleteUsers,
      canViewRoles, canManageRoles,
    });

    return {
      isAuthenticated,
      currentView,
      currentViewComponent,
      navItems,
      navLinkClasses,
      isReadOnly,
      isOwner,
      roleName,
      username,
      prefs,
      t,
      login,
      logout,
    };
  },
};
</script>

<style>
body {
  background-color: #09090b;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}

/* --- Theme variables: surfaces --- */
#app .bg-zinc-950 { background-color: var(--bg-base); }
#app .bg-zinc-900 { background-color: var(--bg-surface); }
#app .bg-zinc-900\/40 { background-color: color-mix(in srgb, var(--bg-surface) 40%, transparent); }
#app .bg-zinc-900\/60 { background-color: color-mix(in srgb, var(--bg-surface) 60%, transparent); }
#app .bg-zinc-900\/80 { background-color: color-mix(in srgb, var(--bg-surface) 80%, transparent); }
#app .bg-zinc-900\/90 { background-color: color-mix(in srgb, var(--bg-surface) 90%, transparent); }
#app .bg-zinc-900\/95 { background-color: color-mix(in srgb, var(--bg-surface) 95%, transparent); }
#app .bg-zinc-800 { background-color: var(--bg-input); }
#app .bg-zinc-800\/60 { background-color: color-mix(in srgb, var(--bg-input) 60%, transparent); }
#app .bg-zinc-800\/80 { background-color: color-mix(in srgb, var(--bg-input) 80%, transparent); }
#app .bg-zinc-700 { background-color: var(--bg-input); }
#app .bg-zinc-700\/40 { background-color: color-mix(in srgb, var(--bg-input) 40%, transparent); }
#app .bg-\[\#09090b\] { background-color: var(--bg-base); }
#app .bg-white\/5 { background-color: color-mix(in srgb, var(--text-main) 5%, transparent); }
#app .bg-white\/10 { background-color: color-mix(in srgb, var(--text-main) 10%, transparent); }
#app .bg-white\/\[0\.03\] { background-color: color-mix(in srgb, var(--text-main) 3%, transparent); }
#app .bg-zinc-950\/80 { background-color: color-mix(in srgb, var(--bg-base) 80%, transparent); }
#app .bg-black\/40 { background-color: var(--bg-input); }

/* --- Theme variables: text --- */
#app .text-white { color: var(--text-main); }
#app .text-white\/80 { color: color-mix(in srgb, var(--text-main) 80%, transparent); }
#app .text-zinc-100 { color: var(--text-2); }
#app .text-zinc-200 { color: var(--text-2); }
#app .text-zinc-300 { color: var(--text-3); }
#app .text-zinc-400 { color: var(--text-4); }
#app .text-zinc-500 { color: var(--text-5); }

/* --- Theme variables: borders --- */
#app .border-white\/10 { border-color: var(--border-soft); }
#app .border-white\/5 { border-color: var(--border-softer); }

/* --- Accent variables: text --- */
#app .text-indigo-100 { color: var(--acc-text); }
#app .text-indigo-300 { color: var(--acc-text); }
#app .text-indigo-400 { color: var(--acc-text-2); }

/* --- Accent variables: surfaces --- */
#app .bg-indigo-400 { background-color: var(--acc); }
#app .bg-indigo-500 { background-color: var(--acc); }
#app .bg-indigo-600 { background-color: var(--acc); }
#app .bg-indigo-500\/15 { background-color: color-mix(in srgb, var(--acc) 15%, transparent); }
#app .bg-indigo-500\/20 { background-color: color-mix(in srgb, var(--acc) 20%, transparent); }
#app .bg-indigo-500\/25 { background-color: color-mix(in srgb, var(--acc) 25%, transparent); }
#app .bg-indigo-600\/15 { background-color: color-mix(in srgb, var(--acc) 15%, transparent); }
#app .bg-indigo-600\/90 { background-color: color-mix(in srgb, var(--acc) 90%, transparent); }

/* --- Accent variables: borders & focus --- */
#app .border-indigo-400\/20 { border-color: color-mix(in srgb, var(--acc) 20%, transparent); }
#app .border-indigo-500\/20 { border-color: color-mix(in srgb, var(--acc) 20%, transparent); }
#app .border-indigo-500\/30 { border-color: color-mix(in srgb, var(--acc) 30%, transparent); }
#app .border-indigo-500\/40 { border-color: color-mix(in srgb, var(--acc) 40%, transparent); }
#app .focus\:border-indigo-500\/50:focus { border-color: color-mix(in srgb, var(--acc) 50%, transparent); }
#app .focus\:ring-indigo-500:focus { --tw-ring-color: var(--acc); }
#app .focus\:ring-indigo-500\/50:focus { --tw-ring-color: color-mix(in srgb, var(--acc) 50%, transparent); }
#app .focus\:ring-offset-\[\#09090b\]:focus { --tw-ring-offset-color: var(--bg-base); }
#app .focus\:border-zinc-500\/40:focus { border-color: color-mix(in srgb, var(--acc) 40%, transparent); }
#app .focus\:ring-zinc-500\/40:focus { --tw-ring-color: color-mix(in srgb, var(--acc) 40%, transparent); }

/* --- Accent variables: shadows & glows --- */
#app .shadow-indigo-500\/10 { --tw-shadow-color: color-mix(in srgb, var(--acc) 10%, transparent); }
#app .shadow-indigo-900\/50 { --tw-shadow-color: var(--acc-dark); }
#app .shadow-indigo-950\/50 { --tw-shadow-color: var(--acc-dark); }
#app .shadow-\[0_0_8px_2px_rgba\(129\,140\,248\,0\.6\)\] { box-shadow: 0 0 8px 2px color-mix(in srgb, var(--acc) 60%, transparent); }

/* --- Semantic colors: danger / success / warning / info ---
   Pastel Tailwind tones (red-200..400, emerald-100..200, amber-200, etc.)
   are unreadable on the light surface, so they map to darkened accents in
   light mode while keeping the current palette in dark mode. */
#app .text-red-200, #app .text-red-300, #app .text-red-400, #app .text-red-500, #app .text-red-700 { color: var(--danger); }
#app .text-red-100 { color: var(--danger-strong); }
#app .text-emerald-100, #app .text-emerald-200, #app .text-emerald-300, #app .text-emerald-400,
#app .text-green-100, #app .text-green-400 { color: var(--success); }
#app .text-amber-200, #app .text-amber-300, #app .text-yellow-300, #app .text-yellow-400 { color: var(--warning); }
#app .text-blue-300, #app .text-blue-400 { color: var(--info); }

#app .bg-red-900\/10, #app .bg-red-900\/20, #app .bg-red-950\/20, #app .bg-red-950\/30 { background-color: color-mix(in srgb, var(--danger) 12%, transparent); }
#app .bg-red-500\/10, #app .bg-red-500\/15, #app .bg-red-500\/20, #app .bg-red-500\/30 { background-color: color-mix(in srgb, var(--danger) 12%, transparent); }
#app .bg-emerald-500\/15, #app .bg-emerald-500\/20, #app .bg-emerald-500\/25, #app .bg-emerald-500\/30,
#app .bg-green-500\/15, #app .bg-green-500\/25 { background-color: color-mix(in srgb, var(--success) 12%, transparent); }
#app .bg-amber-500\/10, #app .bg-yellow-500\/15 { background-color: color-mix(in srgb, var(--warning) 12%, transparent); }

#app .border-red-500\/30, #app .border-red-500\/40, #app .border-red-500\/50 { border-color: color-mix(in srgb, var(--danger) 40%, transparent); }
#app .border-emerald-500\/30, #app .border-emerald-500\/40, #app .border-green-500\/30 { border-color: color-mix(in srgb, var(--success) 40%, transparent); }
#app .border-amber-500\/30 { border-color: color-mix(in srgb, var(--warning) 40%, transparent); }
</style>
