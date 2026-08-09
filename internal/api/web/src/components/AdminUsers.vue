<template>
  <div>
    <div class="flex flex-wrap justify-between items-center gap-3 mb-8">
      <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('users_title') }}<span class="font-mono text-indigo-400">]</span></h1>
      <div class="flex space-x-4">
        <button v-if="canCreateUsers" @click="openAddUserModal" class="flex items-center space-x-2 px-4 py-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">
          <Users class="w-5 h-5" />
          <span class="font-mono text-sm truncate min-w-0">[{{ t('add_user') }}]</span>
        </button>
      </div>
    </div>

    <div class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl overflow-x-auto">
      <table class="min-w-full divide-y divide-white/5">
        <thead class="bg-white/[0.03]">
          <tr>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-400 uppercase tracking-wider">{{ t('users_th_user') }}</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-400 uppercase tracking-wider">{{ t('users_th_role') }}</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-400 uppercase tracking-wider">{{ t('users_th_created') }}</th>
            <th scope="col" class="relative px-6 py-3">
              <span class="sr-only">{{ t('users_th_actions') }}</span>
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-white/5">
          <tr v-for="user in users" :key="user.id" class="hover:bg-white/[0.03] transition-colors">
            <td class="px-6 py-4 whitespace-nowrap">
              <div class="flex items-center">
                <div class="text-sm font-medium text-white">{{ user.username }}</div>
              </div>
            </td>
            <td class="px-6 py-4 whitespace-nowrap">
              <span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full"
                    :style="{ backgroundColor: user.role_color || user.color_hex || '#374151', color: '#ffffff' }">
                {{ user.role_name || user.role }}
              </span>
              <span v-if="userRank(user) !== null" class="ml-2 px-2 py-1 inline-flex text-xs font-semibold rounded-full bg-zinc-700/40 border border-white/10 text-zinc-300">
                [ {{ t('users_rank') }}: {{ userRank(user) }} ]
              </span>
            </td>
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-400">{{ new Date(user.created_at).toLocaleDateString() }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
              <div class="flex items-center justify-end space-x-3">
                <button v-if="canEditUserRow(user)" @click="openEditUser(user)" class="text-blue-400 hover:text-blue-300 transition-colors" :title="t('users_edit_tt')">
                  <Edit class="w-5 h-5" />
                </button>
                <button v-if="canDeleteUserRow(user)" @click="confirmDeleteUser(user)" class="text-red-500 hover:text-red-700 transition-colors" :title="t('users_delete_tt')">
                  <Trash2 class="w-5 h-5" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Add User Modal -->
    <div v-if="showAddUserModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showAddUserModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ showEditUserModal ? t('edit_user') : t('add_user') }}<span class="font-mono text-indigo-400">]</span></h2>
        <form @submit.prevent="handleCreateUser">
          <div class="space-y-4">
            <div>
              <label for="username" class="block text-sm font-medium text-zinc-400">{{ t('users_username') }}</label>
              <input v-model="newUser.username" type="text" id="username" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50" required>
              <p v-if="createError" class="mt-1 text-sm text-red-400">{{ createError }}</p>
            </div>
            <div>
              <label for="password" class="block text-sm font-medium text-zinc-400">{{ t('users_password') }}</label>
              <input v-model="newUser.password" type="password" id="password" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50" required>
            </div>
             <div>
                <label for="role" class="block text-sm font-medium text-zinc-400">{{ t('users_role') }}</label>
                <select v-model="newUser.role_id" id="role" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50" @change="onRoleSelectChange">
                  <option value="" disabled selected>{{ t('users_select_role') }}</option>
                  <option v-for="role in availableRoles" :key="role.id" :value="role.id">{{ role.name }}</option>
                </select>
             </div>
            <div v-if="isOwner">
              <label for="color_hex" class="block text-sm font-medium text-zinc-400">{{ t('users_role_color') }}</label>
              <div class="flex items-center space-x-2 mt-1">
                <input v-model="newUser.color_hex" type="color" id="color_picker" class="h-10 w-16 rounded cursor-pointer bg-zinc-800 border-white/10">
                <input v-model="newUser.color_hex" type="text" id="color_hex" placeholder="#FF5733" class="flex-1 bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
              </div>
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="showAddUserModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
            <button type="submit" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">{{ t('users_create_btn') }}</button>
          </div>
        </form>
      </div>
    </div>

    <!-- Edit User Modal -->
    <div v-if="editingUser" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="editingUser = null">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('users_edit_title') }}<span class="font-mono text-indigo-400">]</span>: {{ editingUser.username }}</h2>
        <form @submit.prevent="handleEditUser">
          <div class="space-y-4">
            <div>
              <label for="edit_username" class="block text-sm font-medium text-zinc-400">{{ t('users_username') }}</label>
              <input v-model="editUserForm.username" type="text" id="edit_username" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
            </div>
            <div>
              <label for="edit_role" class="block text-sm font-medium text-zinc-400">
                {{ t('users_role') }}
                <span v-if="editingUser.username === currentUser?.username" class="inline-flex items-center ml-1 text-zinc-500" :title="t('users_own_role_tt')">
                  <Lock class="w-3.5 h-3.5" />
                </span>
              </label>
              <select v-model="editUserForm.role" id="edit_role" :disabled="editingUser.username === currentUser?.username" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 disabled:opacity-50 disabled:cursor-not-allowed" @change="onEditRoleSelectChange">
                <option value="" disabled>{{ t('users_select_role') }}</option>
                <option v-for="role in availableRoles" :key="role.name" :value="role.name">{{ role.name }}</option>
              </select>
            </div>
            <div>
              <label for="edit_password" class="block text-sm font-medium text-zinc-400">{{ t('users_new_pass') }}</label>
              <input v-model="editUserForm.password" type="password" id="edit_password" :placeholder="t('users_new_pass_ph')" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="editingUser = null" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
            <button type="submit" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">{{ t('users_save') }}</button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, inject, onMounted } from 'vue';
import { Users, Trash2, Plus, Edit, Lock } from 'lucide-vue-next';

// Fallback rank table for built-in roles when the DB rank hasn't loaded yet.
const ROLE_RANK = { owner: 100, admin: 80, client: 30, viewer: 10 };

const roleRank = (role, rolesList) => {
  const normalized = (role || '').toLowerCase();
  if (rolesList && rolesList.length) {
    const match = rolesList.find(r => (r.name || '').toLowerCase() === normalized);
    if (match && match.rank) return match.rank;
  }
  return ROLE_RANK[normalized] ?? 10;
};

export default {
  name: 'AdminUsers',
  components: { Users, Trash2, Plus, Edit, Lock },
  setup() {
    const authCtxRaw = inject('authCtx', {});
    const t = inject('t') || ((k) => k);
    const prefs = inject('prefs', ref({ theme_mode: 'obsidian' }));
    const modalBackdrop = computed(() => prefs.value.theme_mode === 'light' ? 'bg-zinc-900/70 backdrop-blur-md' : 'bg-black/70 backdrop-blur-md');
    const actorRole = computed(() => authCtxRaw.user?.value?.role || '');
    const canCreateUsers = computed(() => authCtxRaw.canCreateUsers?.value ?? false);
    const canEditUsers = computed(() => authCtxRaw.canEditUsers?.value ?? false);
    const canDeleteUsers = computed(() => authCtxRaw.canDeleteUsers?.value ?? false);

    const users = ref([]);
    const customRoles = ref([]);
    const showAddUserModal = ref(false);
    const isOwner = ref(false);
    const editingUser = ref(null);
    const newUser = ref({ username: '', password: '', role_id: '', color_hex: '#374151' });
    const editUserForm = ref({ role: '', password: '' });

    // Actor's effective rank, resolved against the fetched roles list (which
    // carries the configurable DB rank) with a built-in fallback.
    const actorRank = computed(() => roleRank(actorRole.value, customRoles.value));

    // The owner role is reserved for the original admin account; it must never
    // appear in create/edit dropdowns.
    const allRoles = computed(() => {
      return (customRoles.value || []).filter(r => r.name !== 'owner' && r.name !== 'Owner');
    });

    // Only roles ranked STRICTLY LOWER than the actor may be offered for
    // creation/assignment — mirrors the API's hierarchy enforcement.
    const availableRoles = computed(() => {
      return (allRoles.value || []).filter(r => roleRank(r.name, customRoles.value) < actorRank.value);
    });

    const currentUser = ref(null);
    const createError = ref('');

    // Rank for a given user row, resolved from the roles list when possible.
    const userRank = (user) => {
      const roleName = user.role || user.role_name || '';
      if (!roleName) return null;
      try {
        return roleRank(roleName, customRoles.value);
      } catch (_) {
        return null;
      }
    };

    // A target row's actions are only rendered (a) when the actor holds the
    // matching permission AND (b) the target role rank is STRICTLY LOWER than
    // the actor's own rank.
    const canEditUserRow = (user) => {
      return canEditUsers.value && roleRank(user.role || user.role_name, customRoles.value) < actorRank.value;
    };
    const canDeleteUserRow = (user) => {
      return canDeleteUsers.value && roleRank(user.role || user.role_name, customRoles.value) < actorRank.value;
    };

    const openAddUserModal = () => {
      createError.value = '';
      newUser.value = { username: '', password: '', role_id: '', color_hex: '#374151' };
      showAddUserModal.value = true;
    };

    const fetchCurrentUser = async () => {
      try {
        const response = await fetch('/api/auth/me');
        if (response.ok) {
          const user = await response.json();
          currentUser.value = user;
          isOwner.value = user.role === 'owner';
        }
      } catch (e) {
        console.error('Failed to fetch current user:', e);
      }
    };

    const fetchUsers = async () => {
      try {
        const response = await fetch('/api/web/users');
        if (!response.ok) throw new Error('Failed to fetch users');
        users.value = await response.json();
      } catch (error) {
        console.error('Error fetching users:', error);
        alert(t('users_fetch_failed'));
      }
    };

    const fetchCustomRoles = async () => {
      try {
        const response = await fetch('/api/web/roles');
        if (response.ok) {
          customRoles.value = await response.json();
        }
      } catch (e) {
        console.error('Error fetching custom roles:', e);
      }
    };

    const onRoleSelectChange = () => {
      const selectedId = parseInt(newUser.value.role_id);
      const matchingRole = (customRoles.value || []).find(r => r.id === selectedId);
      if (matchingRole) {
        newUser.value.color_hex = matchingRole.color_hex;
      }
    };

    const onEditRoleSelectChange = () => {
      const selectedRoleName = editUserForm.value.role;
      const matchingRole = (customRoles.value || []).find(r => r.name === selectedRoleName);
      // Don't auto-set color in edit mode (user already has one)
    };

    const openEditUser = (user) => {
      editingUser.value = user;
      editUserForm.value = { role: user.role || user.role_name || '', password: '', username: user.username };
    };

    const handleEditUser = async () => {
      if (!editingUser.value) return;
      try {
        const payload = {};
        if (editingUser.value.username !== currentUser.value?.username) {
          payload.role = editUserForm.value.role;
        }
        if (editUserForm.value.username && editUserForm.value.username !== editingUser.value.username) {
          payload.username = editUserForm.value.username;
        }
        if (editUserForm.value.password) {
          payload.password = editUserForm.value.password;
        }
        const response = await fetch(`/api/web/users/${editingUser.value.id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!response.ok) {
          let errMsg = t('users_update_failed');
          try {
            const errData = await response.json();
            if (errData.error) errMsg = errData.error;
          } catch (_) {}
          throw new Error(errMsg);
        }

        editingUser.value = null;
        editUserForm.value = { role: '', password: '' };
        await fetchUsers();
        alert(t('users_updated_ok'));
      } catch (error) {
        console.error('Error updating user:', error);
        alert(error.message || t('users_update_failed'));
      }
    };

    const handleCreateUser = async () => {
      createError.value = '';
      if (!newUser.value.role_id) {
        createError.value = t('users_select_role_required');
        return;
      }
      try {
        const payload = {
          username: newUser.value.username,
          password: newUser.value.password,
          role_id: parseInt(newUser.value.role_id),
          color_hex: newUser.value.color_hex,
        };
        const response = await fetch('/api/web/users', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!response.ok) {
          let errMsg = t('users_create_failed');
          try {
            const errData = await response.json();
            if (errData.error) errMsg = errData.error;
          } catch (_) {}
          createError.value = errMsg;
          return;
        }

        showAddUserModal.value = false;
        newUser.value = { username: '', password: '', role_id: '', color_hex: '#374151' };
        await fetchUsers();
        alert(t('users_created_ok'));
      } catch (error) {
        console.error('Error creating user:', error);
        createError.value = error.message || t('users_create_failed');
      }
    };

    const confirmDeleteUser = (user) => {
      if (confirm(t('users_delete_confirm', { name: user.username }))) {
        deleteUser(user.id);
      }
    };

    const deleteUser = async (userId) => {
      try {
        const response = await fetch(`/api/web/users/${userId}`, {
          method: 'DELETE',
        });
        if (!response.ok) throw new Error('Failed to delete user');
        await fetchUsers();
        alert(t('users_deleted_ok'));
      } catch (error) {
        console.error('Error deleting user:', error);
        alert(t('users_delete_failed'));
      }
    };

    onMounted(() => {
      fetchCurrentUser();
      fetchUsers();
      fetchCustomRoles();
    });

    return {
      users,
      customRoles,
      availableRoles,
      showAddUserModal,
      isOwner,
      currentUser,
      createError,
      openAddUserModal,
      newUser,
      editingUser,
      editUserForm,
      onRoleSelectChange,
      onEditRoleSelectChange,
      openEditUser,
      handleEditUser,
      handleCreateUser,
      confirmDeleteUser,
      canCreateUsers,
      canEditUserRow,
      canDeleteUserRow,
      userRank,
      t,
    };
  },
};
</script>
