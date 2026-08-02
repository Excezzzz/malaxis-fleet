<template>
  <div class="bg-zinc-950 text-white min-h-screen font-sans relative">
    <div v-if="!isAuthenticated">
      <Login @authenticated="login" />
    </div>
    <div v-else class="relative min-h-screen">
      <header class="sticky top-0 z-40 border-b border-white/10 bg-zinc-900">
        <div class="mx-auto max-w-6xl px-4 py-3 flex items-center justify-between gap-4">
          <div class="flex items-center space-x-3 shrink-0">
            <div class="p-2 rounded-xl bg-indigo-500/15 border border-indigo-400/20">
              <Globe class="w-6 h-6 text-indigo-300" />
            </div>
            <div class="leading-tight">
              <h1 class="text-lg font-bold tracking-tight">Malaxis Fleet</h1>
              <span class="bg-red-600/90 text-white text-[10px] px-1.5 py-0.5 rounded font-bold">v2.1-RBAC</span>
            </div>
          </div>
          <div class="flex items-center space-x-3 shrink-0">
            <span v-if="roleName" class="hidden sm:inline text-xs text-zinc-400">{{ username }} · {{ roleName }}</span>
            <button @click="logout" class="flex items-center space-x-2 px-3 py-2 rounded-xl text-sm text-red-400 hover:text-red-300 hover:bg-red-950/30 transition-colors">
              <LogOut class="w-4 h-4" />
              <span>Logout</span>
            </button>
          </div>
        </div>
      </header>

      <main class="mx-auto max-w-6xl p-4 sm:p-8 pt-8 pb-28">
        <div v-if="isReadOnly" class="mb-6 px-4 py-3 rounded-xl border border-amber-500/30 bg-amber-500/10 text-amber-200 text-sm">
          <strong>Read-only mode:</strong> your role only has view access. Management actions are hidden.
        </div>
        <component :is="currentViewComponent" />
      </main>

      <nav v-if="navItems.length > 0" class="fixed bottom-4 left-1/2 -translate-x-1/2 z-50">
        <div class="flex items-center gap-1 rounded-2xl bg-zinc-900 border border-white/10 shadow-2xl shadow-black/40 p-1.5">
          <a v-for="item in navItems" :key="item.view" @click.prevent="currentView = item.view" href="#"
             :title="item.label"
             class="flex items-center space-x-2 px-3 sm:px-4 py-2 rounded-xl text-sm transition-colors"
             :class="navLinkClasses(item.view)">
            <component :is="item.icon" class="w-4 h-4" />
            <span class="hidden lg:inline">{{ item.label }}</span>
          </a>
        </div>
      </nav>
    </div>
  </div>
</template>

<script>
import { ref, computed, onMounted, provide } from 'vue';
import { Globe, Server, Users, FileText, Shield, LogOut, FileCode2, Settings } from 'lucide-vue-next';
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
    Globe, Server, Users, FileText, Shield, LogOut, FileCode2, Settings,
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
      if (role.value === 'owner' || role.value === 'admin') return true;
      return permissions.value.includes(perm);
    };

    const canViewNodes = computed(() => hasPermission('can_view_nodes'));
    const canEditSub = computed(() => hasPermission('can_edit_sub'));
    const canSwitchVpn = computed(() => hasPermission('can_switch_vpn'));
    const canManageUsers = computed(() => hasPermission('can_manage_users'));
    const canViewAudit = computed(() => hasPermission('can_view_audit'));
    const isOwner = computed(() => role.value === 'owner');
    const isReadOnly = computed(() => !canEditSub.value && !canSwitchVpn.value);

    const navItems = computed(() => {
      const items = [];
      if (canViewNodes.value) items.push({ view: 'Nodes', label: 'Nodes', icon: 'Server' });
      if (canEditSub.value) items.push({ view: 'ClientFiles', label: 'Client Files', icon: 'FileCode2' });
      if (canManageUsers.value) items.push({ view: 'AdminUsers', label: 'Fleet Users', icon: 'Users' });
      if (canManageUsers.value) items.push({ view: 'RoleManager', label: 'Roles & Permissions', icon: 'Shield' });
      if (canViewAudit.value) items.push({ view: 'AuditLogs', label: 'Audit Logs', icon: 'FileText' });
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

    provide('authCtx', { canEditSub, canSwitchVpn, canViewNodes, isReadOnly });

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
