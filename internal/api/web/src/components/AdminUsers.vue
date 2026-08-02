<template>
  <div>
    <div class="flex justify-between items-center mb-8">
      <h1 class="text-4xl font-bold tracking-tight">Fleet Users</h1>
      <div class="flex space-x-4">
        <button @click="openAddUserModal" class="flex items-center space-x-2 px-4 py-2 bg-indigo-500/15 hover:bg-indigo-500/25 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">
          <Users class="w-5 h-5" />
          <span>Add New User</span>
        </button>
      </div>
    </div>

    <div class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl overflow-hidden">
      <table class="min-w-full divide-y divide-white/5">
        <thead class="bg-white/[0.03]">
          <tr>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-400 uppercase tracking-wider">User</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-400 uppercase tracking-wider">Role</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-zinc-400 uppercase tracking-wider">Created At</th>
            <th scope="col" class="relative px-6 py-3">
              <span class="sr-only">Actions</span>
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
            </td>
            <td class="px-6 py-4 whitespace-nowrap text-sm text-zinc-400">{{ new Date(user.created_at).toLocaleDateString() }}</td>
            <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
              <div class="flex items-center justify-end space-x-3">
                <button @click="openEditUser(user)" class="text-blue-400 hover:text-blue-300 transition-colors" title="Edit User">
                  <Edit class="w-5 h-5" />
                </button>
                <button @click="confirmDeleteUser(user)" class="text-red-500 hover:text-red-700 transition-colors" title="Delete User">
                  <Trash2 class="w-5 h-5" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Add User Modal -->
    <div v-if="showAddUserModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900/95 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Add New User</h2>
        <form @submit.prevent="handleCreateUser">
          <div class="space-y-4">
            <div>
              <label for="username" class="block text-sm font-medium text-zinc-400">Username</label>
              <input v-model="newUser.username" type="text" id="username" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50" required>
              <p v-if="createError" class="mt-1 text-sm text-red-400">{{ createError }}</p>
            </div>
            <div>
              <label for="password" class="block text-sm font-medium text-zinc-400">Password</label>
              <input v-model="newUser.password" type="password" id="password" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50" required>
            </div>
             <div>
                <label for="role" class="block text-sm font-medium text-zinc-400">Role</label>
                <select v-model="newUser.role_id" id="role" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50" @change="onRoleSelectChange">
                  <option value="" disabled selected>Select a role...</option>
                  <option v-for="role in availableRoles" :key="role.id" :value="role.id">{{ role.name }}</option>
                </select>
             </div>
            <div v-if="isOwner">
              <label for="color_hex" class="block text-sm font-medium text-zinc-400">Role Color HEX</label>
              <div class="flex items-center space-x-2 mt-1">
                <input v-model="newUser.color_hex" type="color" id="color_picker" class="h-10 w-16 rounded cursor-pointer bg-zinc-800 border-white/10">
                <input v-model="newUser.color_hex" type="text" id="color_hex" placeholder="#FF5733" class="flex-1 bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
              </div>
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="showAddUserModal = false" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
            <button type="submit" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Create User</button>
          </div>
        </form>
      </div>
    </div>

    <!-- Edit User Modal -->
    <div v-if="editingUser" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900/95 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Edit User: {{ editingUser.username }}</h2>
        <form @submit.prevent="handleEditUser">
          <div class="space-y-4">
            <div>
              <label for="edit_role" class="block text-sm font-medium text-zinc-400">
                Role
                <span v-if="editingUser.username === currentUser?.username" class="inline-flex items-center ml-1 text-zinc-500" title="Cannot modify your own session role">
                  <Lock class="w-3.5 h-3.5" />
                </span>
              </label>
              <select v-model="editUserForm.role" id="edit_role" :disabled="editingUser.username === currentUser?.username" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50 disabled:opacity-50 disabled:cursor-not-allowed" @change="onEditRoleSelectChange">
                <option value="" disabled>Select a role...</option>
                <option v-for="role in availableRoles" :key="role.name" :value="role.name">{{ role.name }}</option>
              </select>
            </div>
            <div>
              <label for="edit_password" class="block text-sm font-medium text-zinc-400">New Password (leave empty to keep current)</label>
              <input v-model="editUserForm.password" type="password" id="edit_password" placeholder="Enter new password" class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500/50">
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="editingUser = null" class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
            <button type="submit" class="px-4 py-2 bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 text-indigo-100 rounded-xl transition-colors">Save Changes</button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, onMounted } from 'vue';
import { Users, Trash2, Plus, Edit, Lock } from 'lucide-vue-next';

export default {
  name: 'AdminUsers',
  components: { Users, Trash2, Plus, Edit, Lock },
  setup() {
    const users = ref([]);
    const customRoles = ref([]);
    const showAddUserModal = ref(false);
    const isOwner = ref(false);
    const editingUser = ref(null);
    const newUser = ref({ username: '', password: '', role_id: '', color_hex: '#374151' });
    const editUserForm = ref({ role: '', password: '' });

    // The owner role is reserved for the original admin account; it must never
    // appear in create/edit dropdowns.
    const availableRoles = computed(() => {
      return (customRoles.value || []).filter(r => r.name !== 'owner' && r.name !== 'Owner');
    });

    const currentUser = ref(null);
    const createError = ref('');

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
        alert('Could not fetch users.');
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
      editUserForm.value = { role: user.role || user.role_name || '', password: '' };
    };

    const handleEditUser = async () => {
      if (!editingUser.value) return;
      try {
        const payload = {
          role: editUserForm.value.role,
        };
        if (editUserForm.value.password) {
          payload.password = editUserForm.value.password;
        }
        const response = await fetch(`/api/web/users/${editingUser.value.id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!response.ok) {
          let errMsg = 'Failed to update user';
          try {
            const errData = await response.json();
            if (errData.error) errMsg = errData.error;
          } catch (_) {}
          throw new Error(errMsg);
        }

        editingUser.value = null;
        editUserForm.value = { role: '', password: '' };
        await fetchUsers();
        alert('User updated successfully!');
      } catch (error) {
        console.error('Error updating user:', error);
        alert(error.message || 'Could not update user.');
      }
    };

    const handleCreateUser = async () => {
      createError.value = '';
      if (!newUser.value.role_id) {
        createError.value = 'Please select a role.';
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
          let errMsg = 'Failed to create user';
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
        alert('User created successfully!');
      } catch (error) {
        console.error('Error creating user:', error);
        createError.value = error.message || 'Could not create user.';
      }
    };

    const confirmDeleteUser = (user) => {
      if (confirm(`Are you sure you want to delete the user "${user.username}"?`)) {
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
        alert('User deleted successfully!');
      } catch (error) {
        console.error('Error deleting user:', error);
        alert('Could not delete user.');
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
    };
  },
};
</script>
