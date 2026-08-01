<template>
  <div>
    <div class="flex justify-between items-center mb-8">
      <h1 class="text-4xl font-bold">Admin Users</h1>
      <div class="flex space-x-4">
        <button @click="openAddUserModal" class="flex items-center space-x-2 px-4 py-2 bg-indigo-600 hover:bg-indigo-700 rounded-lg transition-colors">
          <Users class="w-5 h-5" />
          <span>Add New User</span>
        </button>
        <button @click="showRoleModal = true" v-if="isOwner" class="flex items-center space-x-2 px-4 py-2 bg-purple-600 hover:bg-purple-700 rounded-lg transition-colors">
          <Shield class="w-5 h-5" />
          <span>🎭 Custom Roles & Permissions</span>
        </button>
      </div>
    </div>

    <div class="bg-gray-800 border border-gray-700 rounded-lg shadow">
      <table class="min-w-full divide-y divide-gray-700">
        <thead class="bg-gray-850">
          <tr>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">User</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Role</th>
            <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Created At</th>
            <th scope="col" class="relative px-6 py-3">
              <span class="sr-only">Actions</span>
            </th>
          </tr>
        </thead>
        <tbody class="bg-gray-800 divide-y divide-gray-700">
          <tr v-for="user in users" :key="user.id">
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
            <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-400">{{ new Date(user.created_at).toLocaleDateString() }}</td>
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
    <div v-if="showAddUserModal" class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center">
      <div class="bg-gray-800 rounded-lg p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6">Add New User</h2>
        <form @submit.prevent="handleCreateUser">
          <div class="space-y-4">
            <div>
              <label for="username" class="block text-sm font-medium text-gray-400">Username</label>
              <input v-model="newUser.username" type="text" id="username" class="mt-1 block w-full bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500" required>
              <p v-if="createError" class="mt-1 text-sm text-red-400">{{ createError }}</p>
            </div>
            <div>
              <label for="password" class="block text-sm font-medium text-gray-400">Password</label>
              <input v-model="newUser.password" type="password" id="password" class="mt-1 block w-full bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500" required>
            </div>
             <div>
                <label for="role" class="block text-sm font-medium text-gray-400">Role</label>
                <select v-model="newUser.role_id" id="role" class="mt-1 block w-full bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500" @change="onRoleSelectChange">
                  <option value="" disabled selected>Select a role...</option>
                  <option v-for="role in availableRoles" :key="role.id" :value="role.id">{{ role.name }}</option>
                </select>
             </div>
            <div v-if="isOwner">
              <label for="color_hex" class="block text-sm font-medium text-gray-400">Role Color HEX</label>
              <div class="flex items-center space-x-2 mt-1">
                <input v-model="newUser.color_hex" type="color" id="color_picker" class="h-10 w-16 rounded cursor-pointer bg-gray-700 border-gray-600">
                <input v-model="newUser.color_hex" type="text" id="color_hex" placeholder="#FF5733" class="flex-1 bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500">
              </div>
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="showAddUserModal = false" class="px-4 py-2 bg-gray-600 hover:bg-gray-700 rounded-lg transition-colors">Cancel</button>
            <button type="submit" class="px-4 py-2 bg-indigo-600 hover:bg-indigo-700 rounded-lg transition-colors">Create User</button>
          </div>
        </form>
      </div>
    </div>

    <!-- Edit User Modal -->
    <div v-if="editingUser" class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center">
      <div class="bg-gray-800 rounded-lg p-8 w-full max-w-md">
        <h2 class="text-2xl font-bold mb-6">Edit User: {{ editingUser.username }}</h2>
        <form @submit.prevent="handleEditUser">
          <div class="space-y-4">
            <div>
              <label for="edit_role" class="block text-sm font-medium text-gray-400">
                Role
                <span v-if="editingUser.username === currentUser?.username" class="inline-flex items-center ml-1 text-gray-500" title="Cannot modify your own session role">
                  <Lock class="w-3.5 h-3.5" />
                </span>
              </label>
              <select v-model="editUserForm.role" id="edit_role" :disabled="editingUser.username === currentUser?.username" class="mt-1 block w-full bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed" @change="onEditRoleSelectChange">
                <option value="" disabled>Select a role...</option>
                <option v-for="role in availableRoles" :key="role.name" :value="role.name">{{ role.name }}</option>
              </select>
            </div>
            <div>
              <label for="edit_password" class="block text-sm font-medium text-gray-400">New Password (leave empty to keep current)</label>
              <input v-model="editUserForm.password" type="password" id="edit_password" placeholder="Enter new password" class="mt-1 block w-full bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500">
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="editingUser = null" class="px-4 py-2 bg-gray-600 hover:bg-gray-700 rounded-lg transition-colors">Cancel</button>
            <button type="submit" class="px-4 py-2 bg-indigo-600 hover:bg-indigo-700 rounded-lg transition-colors">Save Changes</button>
          </div>
        </form>
      </div>
    </div>

    <!-- Custom Role Creator Modal -->
    <div v-if="showRoleModal" class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div class="bg-gray-800 rounded-lg p-8 w-full max-w-lg max-h-[90vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-6">🎭 Manage Custom Roles & Permissions</h2>

        <div class="space-y-4 mb-6">
          <h3 class="text-lg font-semibold text-gray-300">Existing Roles</h3>
          <div v-for="role in customRoles" :key="role.id" class="flex items-center justify-between bg-gray-700 rounded-lg px-4 py-2">
            <div class="flex items-center space-x-3">
              <span class="w-4 h-4 rounded-full" :style="{ backgroundColor: role.color_hex }"></span>
              <span>{{ role.name }}</span>
            </div>
            <button @click="deleteRole(role.id)" class="text-red-500 hover:text-red-700">
              <Trash2 class="w-4 h-4" />
            </button>
          </div>
          <div v-if="customRoles.length === 0" class="text-gray-500 text-sm">No custom roles created yet.</div>
        </div>

        <h3 class="text-lg font-semibold text-gray-300 mb-4">Create New Role</h3>
        <form @submit.prevent="handleCreateRole">
          <div class="space-y-4">
            <div>
              <label for="role_name" class="block text-sm font-medium text-gray-400">Role Name</label>
              <input v-model="newRole.name" type="text" id="role_name" placeholder="e.g. Co-Creator, Moderator" class="mt-1 block w-full bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500" required>
            </div>
            <div>
              <label for="role_color" class="block text-sm font-medium text-gray-400">HEX Color</label>
              <div class="flex items-center space-x-2 mt-1">
                <input v-model="newRole.color_hex" type="color" id="role_color_picker" class="h-10 w-16 rounded cursor-pointer bg-gray-700 border-gray-600">
                <input v-model="newRole.color_hex" type="text" id="role_color_hex" placeholder="#FF5733" class="flex-1 bg-gray-700 border-gray-600 rounded-md shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-indigo-500 focus:border-indigo-500">
              </div>
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-400 mb-2">Permissions</label>
              <div class="space-y-2 bg-gray-700 rounded-lg p-3">
                <label class="flex items-center space-x-3">
                  <input type="checkbox" v-model="newRole.permissions" value="can_view_nodes" class="rounded bg-gray-600 text-indigo-500 focus:ring-indigo-500">
                  <span class="text-sm text-gray-300">View Nodes</span>
                </label>
                <label class="flex items-center space-x-3">
                  <input type="checkbox" v-model="newRole.permissions" value="can_switch_vpn" class="rounded bg-gray-600 text-indigo-500 focus:ring-indigo-500">
                  <span class="text-sm text-gray-300">Switch VPN</span>
                </label>
                <label class="flex items-center space-x-3">
                  <input type="checkbox" v-model="newRole.permissions" value="can_edit_sub" class="rounded bg-gray-600 text-indigo-500 focus:ring-indigo-500">
                  <span class="text-sm text-gray-300">Edit Subscription URL</span>
                </label>
                <label class="flex items-center space-x-3">
                  <input type="checkbox" v-model="newRole.permissions" value="can_manage_users" class="rounded bg-gray-600 text-indigo-500 focus:ring-indigo-500">
                  <span class="text-sm text-gray-300">Manage Users</span>
                </label>
                <label class="flex items-center space-x-3">
                  <input type="checkbox" v-model="newRole.permissions" value="can_view_audit" class="rounded bg-gray-600 text-indigo-500 focus:ring-indigo-500">
                  <span class="text-sm text-gray-300">View Audit Logs</span>
                </label>
                <label class="flex items-center space-x-3">
                  <input type="checkbox" v-model="newRole.permissions" value="can_export_backups" class="rounded bg-gray-600 text-indigo-500 focus:ring-indigo-500">
                  <span class="text-sm text-gray-300">Export Backups</span>
                </label>
              </div>
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="showRoleModal = false" class="px-4 py-2 bg-gray-600 hover:bg-gray-700 rounded-lg transition-colors">Close</button>
            <button type="submit" class="px-4 py-2 bg-purple-600 hover:bg-purple-700 rounded-lg transition-colors">Create Role</button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, onMounted } from 'vue';
import { Users, Trash2, Shield, Plus, Edit, Lock } from 'lucide-vue-next';

export default {
  name: 'AdminUsers',
  components: { Users, Trash2, Shield, Plus, Edit, Lock },
  setup() {
    const users = ref([]);
    const customRoles = ref([]);
    const showAddUserModal = ref(false);
    const showRoleModal = ref(false);
    const isOwner = ref(false);
    const editingUser = ref(null);
    const newUser = ref({ username: '', password: '', role_id: '', color_hex: '#374151' });
    const newRole = ref({ name: '', color_hex: '#FF5733', permissions: [] });
    const editUserForm = ref({ role: '', password: '' });

    const availableRoles = computed(() => {
      return customRoles.value || [];
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

    const handleCreateRole = async () => {
      try {
        const roleData = {
          name: newRole.value.name,
          color_hex: newRole.value.color_hex,
          permissions_json: JSON.stringify(newRole.value.permissions),
        };
        const response = await fetch('/api/web/roles', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(roleData),
        });
        if (!response.ok) throw new Error('Failed to create role');

        newRole.value = { name: '', color_hex: '#FF5733', permissions: [] };
        await fetchCustomRoles();
        alert('Custom role created successfully!');
      } catch (error) {
        console.error('Error creating role:', error);
        alert('Could not create role.');
      }
    };

    const deleteRole = async (roleId) => {
      if (!confirm('Are you sure you want to delete this custom role?')) return;
      try {
        const response = await fetch(`/api/web/roles/${roleId}`, {
          method: 'DELETE',
        });
        if (!response.ok) throw new Error('Failed to delete role');
        await fetchCustomRoles();
      } catch (error) {
        console.error('Error deleting role:', error);
        alert('Could not delete role.');
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
      showRoleModal,
      isOwner,
      currentUser,
      createError,
      openAddUserModal,
      newUser,
      newRole,
      editingUser,
      editUserForm,
      onRoleSelectChange,
      onEditRoleSelectChange,
      openEditUser,
      handleEditUser,
      handleCreateUser,
      handleCreateRole,
      deleteRole,
      confirmDeleteUser,
    };
  },
};
</script>
<style>
.bg-gray-850 {
    background-color: #1f2937;
}
</style>
