<template>
  <div class="bg-[#09090b] text-white min-h-screen font-sans relative">
    <div class="fixed inset-0 pointer-events-none bg-[radial-gradient(ellipse_at_top,rgba(99,102,241,0.10),transparent_55%)]"></div>
    <div v-if="!isAuthenticated">
      <Login @authenticated="login" />
    </div>
    <div v-else class="relative min-h-screen">
      <header class="sticky top-4 z-40 mx-auto max-w-6xl mt-4 rounded-3xl bg-zinc-900/60 backdrop-blur-xl border border-white/10 shadow-2xl p-3 px-6">
        <div class="flex items-center justify-between gap-4">
          <div class="flex items-center space-x-3 shrink-0">
            <div class="p-2 rounded-2xl bg-indigo-500/15 border border-indigo-400/20">
              <Globe class="w-6 h-6 text-indigo-300" />
            </div>
            <div class="leading-tight">
              <h1 class="text-lg font-bold tracking-tight">Malaxis Fleet</h1>
              <span class="bg-red-600/90 text-white text-[10px] px-1.5 py-0.5 rounded font-bold">v2.1-RBAC</span>
            </div>
          </div>
          <nav class="hidden md:flex items-center space-x-1 overflow-x-auto">
            <a @click.prevent="currentView = 'Nodes'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200" :class="navLinkClasses('Nodes')">
              <Server class="w-4 h-4" />
              <span>Nodes</span>
            </a>
            <a @click.prevent="currentView = 'ClientFiles'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200" :class="navLinkClasses('ClientFiles')">
              <FileCode2 class="w-4 h-4" />
              <span>Client Files</span>
            </a>
            <a @click.prevent="currentView = 'AdminUsers'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200" :class="navLinkClasses('AdminUsers')">
              <Users class="w-4 h-4" />
              <span>Admin Users</span>
            </a>
            <a @click.prevent="currentView = 'RoleManager'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200" :class="navLinkClasses('RoleManager')">
              <Shield class="w-4 h-4" />
              <span>Roles &amp; Permissions</span>
            </a>
            <a @click.prevent="currentView = 'AuditLogs'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200" :class="navLinkClasses('AuditLogs')">
              <FileText class="w-4 h-4" />
              <span>Audit Logs</span>
            </a>
            <a v-if="isOwner" @click.prevent="currentView = 'Settings'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200" :class="navLinkClasses('Settings')">
              <Settings class="w-4 h-4" />
              <span>Settings</span>
            </a>
          </nav>
          <button @click="logout" class="flex items-center space-x-2 px-3 py-2 rounded-xl text-sm text-zinc-400 hover:bg-white/10 hover:text-white transition-colors shrink-0">
            <Shield class="w-4 h-4" />
            <span>Logout</span>
          </button>
        </div>
        <nav class="md:hidden flex items-center space-x-1 overflow-x-auto mt-3 pt-3 border-t border-white/5">
          <a @click.prevent="currentView = 'Nodes'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200 shrink-0" :class="navLinkClasses('Nodes')">
            <Server class="w-4 h-4" />
            <span>Nodes</span>
          </a>
          <a @click.prevent="currentView = 'ClientFiles'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200 shrink-0" :class="navLinkClasses('ClientFiles')">
            <FileCode2 class="w-4 h-4" />
            <span>Client Files</span>
          </a>
          <a @click.prevent="currentView = 'AdminUsers'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200 shrink-0" :class="navLinkClasses('AdminUsers')">
            <Users class="w-4 h-4" />
            <span>Admin Users</span>
          </a>
          <a @click.prevent="currentView = 'RoleManager'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200 shrink-0" :class="navLinkClasses('RoleManager')">
            <Shield class="w-4 h-4" />
            <span>Roles</span>
          </a>
          <a @click.prevent="currentView = 'AuditLogs'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200 shrink-0" :class="navLinkClasses('AuditLogs')">
            <FileText class="w-4 h-4" />
            <span>Audit Logs</span>
          </a>
          <a v-if="isOwner" @click.prevent="currentView = 'Settings'" href="#" class="flex items-center space-x-2 px-4 py-2 rounded-xl text-sm transition-all duration-200 shrink-0" :class="navLinkClasses('Settings')">
            <Settings class="w-4 h-4" />
            <span>Settings</span>
          </a>
        </nav>
      </header>

      <main class="mx-auto max-w-6xl p-4 sm:p-8 pt-8">
        <component :is="currentViewComponent" />
      </main>
    </div>
  </div>
</template>

<script>
import { ref, computed, onMounted } from 'vue';
import { Globe, Server, Users, FileText, Shield, Settings, FileCode2 } from 'lucide-vue-next';
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
    Globe, Server, Users, FileText, Shield, Settings, FileCode2,
    Login,
    Nodes,
    ClientFiles,
    AdminUsers,
    RoleManager,
    AuditLogs,
    SettingsView,
  },
  setup() {
    const isAuthenticated = ref(false); // Should be false by default
    const currentView = ref('Nodes');
    const isOwner = ref(false);
    const isAdmin = ref(false);

    const currentViewComponent = computed(() => {
      switch (currentView.value) {
        case 'Nodes':
          return 'Nodes';
        case 'ClientFiles':
          return 'ClientFiles';
        case 'AdminUsers':
          return 'AdminUsers';
        case 'RoleManager':
          return 'RoleManager';
        case 'AuditLogs':
          return 'AuditLogs';
        case 'Settings':
          return 'SettingsView';
        default:
          return 'Nodes';
      }
    });

    const navLinkClasses = (viewName) => {
        return currentView.value === viewName
            ? 'bg-white/10 text-white font-semibold shadow-inner'
            : 'text-zinc-400 hover:bg-white/5 hover:text-white transition-all duration-200';
    };

    const login = () => {
        isAuthenticated.value = true;
        // Fetch current user to check if owner
        fetch('/api/auth/me')
            .then(r => r.json())
            .then(user => {
                isOwner.value = user.role === 'owner';
                isAdmin.value = user.role === 'owner' || user.role === 'admin';
            })
            .catch(e => console.error('Failed to fetch current user:', e));
    };
    
    const checkAuth = () => {
        fetch('/api/auth/me')
            .then(r => {
                if (r.ok) {
                    isAuthenticated.value = true;
                    return r.json();
                }
                throw new Error('Not authenticated');
            })
            .then(user => {
                isOwner.value = user.role === 'owner';
                isAdmin.value = user.role === 'owner' || user.role === 'admin';
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
        isOwner.value = false;
        isAdmin.value = false;
        fetch('/api/auth/logout', { method: 'POST' }).catch(() => {});
    };

    return {
      isAuthenticated,
      currentView,
      currentViewComponent,
      navLinkClasses,
      isOwner,
      isAdmin,
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
