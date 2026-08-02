<template>
  <div>
    <div class="flex justify-between items-center mb-8">
      <h1 class="text-4xl font-bold tracking-tight">Custom Roles & Permissions</h1>
      <button @click="showCreateModal = true" class="flex items-center space-x-2 px-4 py-2 bg-purple-500/15 hover:bg-purple-500/25 border border-purple-500/30 text-purple-100 rounded-xl transition-colors">
        <Plus class="w-5 h-5" />
        <span>Create New Role</span>
      </button>
    </div>

    <!-- Existing Roles -->
    <div v-if="customRoles.length > 0" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
      <div v-for="role in customRoles" :key="role.id" class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl p-6 shadow-lg shadow-black/10 hover:border-indigo-500/20 transition-colors">
          <div class="flex items-center justify-between mb-4">
            <div class="flex items-center space-x-3">
              <span class="w-5 h-5 rounded-full" :style="{ backgroundColor: role.color_hex }"></span>
              <h3 class="text-xl font-bold text-white">{{ role.name }}</h3>
            </div>
            <div class="flex space-x-2">
              <button @click="openEditModal(role)" class="text-blue-400 hover:text-blue-300 transition-colors" title="Edit Role">
                <Edit class="w-5 h-5" />
              </button>
              <button @click="deleteRole(role)" class="text-red-500 hover:text-red-700 transition-colors" title="Delete Role">
                <Trash2 class="w-5 h-5" />
              </button>
            </div>
          </div>

        <div class="mb-3">
          <span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full"
                :style="{ backgroundColor: role.color_hex, color: '#ffffff' }">
            {{ role.name }}
          </span>
        </div>

        <div class="space-y-2">
          <p class="text-xs uppercase tracking-wider font-medium text-zinc-500">Permissions:</p>
          <div v-if="parsePermissions(role.permissions_json).length > 0" class="flex flex-wrap gap-2">
            <span v-for="perm in parsePermissions(role.permissions_json)" :key="perm" class="px-2 py-1 bg-white/5 border border-white/10 text-xs text-zinc-300 rounded">
              {{ permLabel(perm) }}
            </span>
          </div>
          <p v-else class="text-sm text-zinc-500">No permissions assigned</p>
        </div>

        <div class="mt-4 text-xs text-zinc-500">
          Created: {{ new Date(role.created_at).toLocaleDateString() }}
        </div>
      </div>
    </div>

    <div v-else class="text-center py-16">
      <Shield class="w-16 h-16 text-zinc-600 mx-auto mb-4" />
      <p class="text-zinc-500 text-lg">No custom roles created yet.</p>
      <p class="text-zinc-600 text-sm mt-2">Create a custom role to assign granular permissions to users.</p>
    </div>

    <!-- Create Role Modal -->
    <div v-if="showCreateModal" class="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div class="bg-zinc-900/95 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-2xl max-h-[90vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-6 tracking-tight">Create New Custom Role</h2>

        <form @submit.prevent="handleCreateRole">
          <div class="space-y-6">
            <div>
              <label for="role_name" class="block text-sm font-medium text-zinc-400 mb-1">Role Name</label>
              <input v-model="newRole.name" type="text" id="role_name" placeholder="e.g. Co-Creator, Moderator"
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50" required>
            </div>
            <div>
              <label for="role_color" class="block text-sm font-medium text-zinc-400 mb-1">Role Color</label>
              <div class="flex items-center space-x-3 mt-1">
                <input v-model="newRole.color_hex" type="color" id="role_color_picker"
                       class="h-10 w-16 rounded cursor-pointer bg-zinc-800 border-white/10">
                <input v-model="newRole.color_hex" type="text" id="role_color_hex" placeholder="#FF5733"
                       class="flex-1 bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50">
              </div>
            </div>
            <div>
              <label class="block text-sm font-medium text-zinc-400 mb-2">Permissions</label>
              <div class="space-y-3 bg-white/5 border border-white/10 rounded-xl p-4 grid grid-cols-1 md:grid-cols-2 gap-3">
                <label v-for="perm in Object.keys(PERMISSION_LABELS)" :key="perm" class="flex items-center space-x-3">
                  <input type="checkbox" v-model="newRole.permissions" :value="perm"
                         class="rounded bg-zinc-700 text-purple-500 focus:ring-purple-500">
                  <span class="text-sm text-zinc-300">{{ PERMISSION_LABELS[perm] }}</span>
                </label>
              </div>
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="showCreateModal = false"
                    class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
            <button type="submit"
                    class="px-4 py-2 bg-purple-500/20 hover:bg-purple-500/30 border border-purple-500/30 text-purple-100 rounded-xl transition-colors">Create Role</button>
          </div>
        </form>
      </div>
    </div>

    <!-- Edit Role Modal -->
    <div v-if="editingRole" class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div class="bg-zinc-900/95 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-8 w-full max-w-2xl max-h-[90vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-6">Edit Role: {{ editingRole.name }}</h2>

        <form @submit.prevent="handleEditRole">
          <div class="space-y-6">
            <div>
              <label for="edit_role_name" class="block text-sm font-medium text-zinc-400 mb-1">Role Name</label>
              <input v-model="editForm.name" type="text" id="edit_role_name"
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50" required>
            </div>
            <div>
              <label for="edit_role_color" class="block text-sm font-medium text-zinc-400 mb-1">Role Color</label>
              <div class="flex items-center space-x-3 mt-1">
                <input v-model="editForm.color_hex" type="color" id="edit_role_color_picker"
                       class="h-10 w-16 rounded cursor-pointer bg-zinc-800 border-white/10">
                <input v-model="editForm.color_hex" type="text" id="edit_role_color_hex" placeholder="#FF5733"
                       class="flex-1 bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50">
              </div>
            </div>
            <div>
              <label class="block text-sm font-medium text-zinc-400 mb-2">Permissions</label>
              <div class="space-y-3 bg-white/5 border border-white/10 rounded-xl p-4 grid grid-cols-1 md:grid-cols-2 gap-3">
                <label v-for="perm in Object.keys(PERMISSION_LABELS)" :key="perm" class="flex items-center space-x-3">
                  <input type="checkbox" v-model="editForm.permissions" :value="perm"
                         class="rounded bg-zinc-700 text-purple-500 focus:ring-purple-500">
                  <span class="text-sm text-zinc-300">{{ PERMISSION_LABELS[perm] }}</span>
                </label>
              </div>
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="editingRole = null"
                    class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">Cancel</button>
            <button type="submit"
                    class="px-4 py-2 bg-purple-500/20 hover:bg-purple-500/30 border border-purple-500/30 text-purple-100 rounded-xl transition-colors">Save Changes</button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, onMounted } from 'vue';
import { Plus, Trash2, Shield, Edit } from 'lucide-vue-next';

const PERMISSION_LABELS = {
  can_view_nodes: 'View Nodes',
  can_switch_vpn: 'Switch VPN',
  can_edit_sub: 'Edit Subscription URL',
  can_rename_node: 'Rename Nodes',
  can_terminate_node: 'Terminate Nodes',
  can_update_client: 'Update Client Files (OTA)',
  can_purge_nodes: 'Purge Offline Nodes',
  can_manage_users: 'Manage Users',
  can_manage_roles: 'Manage Roles',
  can_view_audit: 'View Audit Logs',
  can_export_backups: 'Export Backups',
};

const ALL_PERMS = Object.keys(PERMISSION_LABELS);

export default {
  name: 'RoleManager',
  components: { Plus, Trash2, Shield, Edit },
  setup() {
    const customRoles = ref([]);
    const showCreateModal = ref(false);
    const editingRole = ref(null);
    const newRole = ref({ name: '', color_hex: '#FF5733', permissions: [] });
    const editForm = ref({ name: '', color_hex: '#FF5733', permissions: [] });

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

    const parsePermissions = (permissionsJson) => {
      if (!permissionsJson) return [];
      try {
        const parsed = JSON.parse(permissionsJson);
        if (Array.isArray(parsed)) return parsed;
        if (typeof parsed === 'object' && parsed !== null) return ALL_PERMS.filter(p => parsed[p] === true);
        return [];
      } catch (e) {
        return permissionsJson.split(',').filter(p => p.trim());
      }
    };

    const permLabel = (perm) => PERMISSION_LABELS[perm] || perm;

    const handleCreateRole = async () => {
      try {
        const permsObj = newRole.value.permissions.reduce((acc, p) => ({ ...acc, [p]: true }), {});
        const roleData = {
          name: newRole.value.name,
          color_hex: newRole.value.color_hex,
          permissions_json: JSON.stringify(permsObj),
        };
        const response = await fetch('/api/web/roles', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(roleData),
        });
        if (!response.ok) throw new Error('Failed to create role');

        newRole.value = { name: '', color_hex: '#FF5733', permissions: [] };
        showCreateModal.value = false;
        await fetchCustomRoles();
        alert('Custom role created successfully!');
      } catch (error) {
        console.error('Error creating role:', error);
        alert('Could not create role.');
      }
    };

    const openEditModal = (role) => {
      editingRole.value = role;
      editForm.value = {
        id: role.id,
        name: role.name,
        color_hex: role.color_hex,
        permissions: [...parsePermissions(role.permissions_json)],
      };
    };

    const handleEditRole = async () => {
      if (!editingRole.value) return;
      try {
        const permsObj = editForm.value.permissions.reduce((acc, p) => ({ ...acc, [p]: true }), {});
        const roleData = {
          name: editForm.value.name,
          color_hex: editForm.value.color_hex,
          permissions_json: JSON.stringify(permsObj),
        };
        const response = await fetch(`/api/web/roles/${editForm.value.id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(roleData),
        });
        if (!response.ok) throw new Error('Failed to update role');

        editingRole.value = null;
        await fetchCustomRoles();
        alert('Custom role updated successfully!');
      } catch (error) {
        console.error('Error updating role:', error);
        alert('Could not update role.');
      }
    };

    const deleteRole = async (role) => {
      if (!confirm(`Delete the role "${role.name}"? This action cannot be undone.`)) return;
      try {
        const response = await fetch(`/api/web/roles/${role.id}`, {
          method: 'DELETE',
        });
        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || 'Failed to delete role');
        }
        await fetchCustomRoles();
      } catch (error) {
        console.error('Error deleting role:', error);
        alert(error.message || 'Could not delete role.');
      }
    };

    onMounted(() => {
      fetchCustomRoles();
    });

    return {
      customRoles,
      showCreateModal,
      editingRole,
      newRole,
      editForm,
      parsePermissions,
      permLabel,
      PERMISSION_LABELS,
      handleCreateRole,
      openEditModal,
      handleEditRole,
      deleteRole,
    };
  },
};
</script>
