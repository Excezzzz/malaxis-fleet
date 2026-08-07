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
          <button @click="logout" class="flex items-center justify-center w-9 h-9 rounded-full text-red-400 hover:text-red-300 hover:bg-red-950/30 transition-colors" title="Logout">
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
            <span class="hidden sm:inline-block bg-indigo-600/90 text-white font-bold px-2.5 py-0.5 text-xs rounded-md tracking-wider shadow-lg shadow-indigo-950/50">v2.1.0</span>
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
            <span class="hidden min-[420px]:inline">Logout</span>
          </button>
        </div>
      </nav>

      <main class="w-full px-4 md:px-8 pt-16 md:pt-28 pb-24 md:pb-16">
        <div v-if="isReadOnly" class="mb-6 px-4 py-3 rounded-xl border border-amber-500/30 bg-amber-500/10 text-amber-200 text-sm">
          <strong>Read-only mode:</strong> your role only has view access. Management actions are hidden.
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
      if (canViewNodes.value) items.push({ view: 'Nodes', label: 'Nodes', icon: 'Server' });
      if (canEditSub.value || canUpdateClient.value) items.push({ view: 'ClientFiles', label: 'Client Files', icon: 'FileCode' });
      if (canViewUsers.value) items.push({ view: 'AdminUsers', label: 'Fleet Users', icon: 'Users' });
      if (canViewRoles.value) items.push({ view: 'RoleManager', label: 'Roles & Permissions', icon: 'Shield' });
      if (canViewAuditLogs.value || canViewMasterLogs.value) items.push({ view: 'AuditLogs', label: 'Logs & Audit', icon: 'Terminal' });
      if (isOwner.value) items.push({ view: 'Settings', label: 'Settings', icon: 'Settings' });
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
            })
            .catch(() => {
                isAuthenticated.value = false;
            });
    };

    onMounted(() => {
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
</style>
