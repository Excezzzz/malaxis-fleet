<template>
  <div class="bg-gray-900 text-white min-h-screen font-sans">
    <div v-if="!isAuthenticated">
      <Login @authenticated="login" />
    </div>
    <div v-else class="flex min-h-screen">
      <aside class="w-64 bg-gray-950 p-6 space-y-6 border-r border-gray-800">
        <div class="flex items-center space-x-3 mb-10">
          <Globe class="w-8 h-8 text-indigo-500" />
          <h1 class="text-2xl font-bold">Malaxis Fleet</h1>
          <span class="bg-red-600 text-white text-xs px-2 py-0.5 rounded font-bold">v2.1-RBAC</span>
        </div>
        <nav class="space-y-2">
          <a @click.prevent="currentView = 'Nodes'" href="#" class="flex items-center space-x-3 px-4 py-2 rounded-lg" :class="navLinkClasses('Nodes')">
            <Server class="w-5 h-5" />
            <span>Nodes</span>
          </a>
          <a @click.prevent="currentView = 'AdminUsers'" href="#" class="flex items-center space-x-3 px-4 py-2 rounded-lg" :class="navLinkClasses('AdminUsers')">
            <Users class="w-5 h-5" />
            <span>Admin Users</span>
          </a>
          <a @click.prevent="currentView = 'RoleManager'" href="#" class="flex items-center space-x-3 px-4 py-2 rounded-lg" :class="navLinkClasses('RoleManager')">
            <Shield class="w-5 h-5" />
            <span>🎭 Roles & Permissions</span>
          </a>
          <a @click.prevent="currentView = 'AuditLogs'" href="#" class="flex items-center space-x-3 px-4 py-2 rounded-lg" :class="navLinkClasses('AuditLogs')">
            <FileText class="w-5 h-5" />
            <span>Audit Logs</span>
          </a>
          <a @click.prevent="currentView = 'Settings'" href="#" class="flex items-center space-x-3 px-4 py-2 rounded-lg" :class="navLinkClasses('Settings')" v-if="isOwner">
            <Settings class="w-5 h-5" />
            <span>Settings</span>
          </a>
        </nav>
        <div class="absolute bottom-6">
            <button @click="logout" class="flex items-center space-x-3 px-4 py-2 rounded-lg text-gray-400 hover:bg-gray-800 hover:text-white transition-colors duration-200">
                <Shield class="w-5 h-5" />
                <span>Logout</span>
            </button>
        </div>
      </aside>

      <main class="flex-1 p-8">
        <component :is="currentViewComponent" />
      </main>
    </div>
  </div>
</template>

<script>
import { ref, computed, onMounted } from 'vue';
import { Globe, Server, Users, FileText, Shield, Settings } from 'lucide-vue-next';
import Login from './components/Login.vue';
import Nodes from './components/Nodes.vue';
import AdminUsers from './components/AdminUsers.vue';
import RoleManager from './components/RoleManager.vue';
import AuditLogs from './components/AuditLogs.vue';
import SettingsView from './components/Settings.vue';

export default {
  name: 'App',
  components: {
    Globe, Server, Users, FileText, Shield, Settings,
    Login,
    Nodes,
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
            ? 'bg-gray-800 text-white font-semibold'
            : 'text-gray-400 hover:bg-gray-800 hover:text-white transition-colors duration-200';
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
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}
</style>
