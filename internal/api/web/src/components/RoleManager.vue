<template>
  <div>
    <div class="flex flex-wrap justify-between items-center gap-3 mb-8">
      <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>Roles &amp; Permissions<span class="font-mono text-indigo-400">]</span></h1>
      <button v-if="canManageRoles" @click="showCreateModal = true" class="flex items-center space-x-2 px-4 py-2 bg-purple-500/15 hover:bg-purple-500/25 border border-purple-500/30 text-purple-100 rounded-xl transition-colors">
        <Plus class="w-5 h-5" />
        <span class="font-mono text-sm">[Create New Role]</span>
      </button>
    </div>

    <!-- Existing Roles -->
    <div v-if="customRoles.length > 0" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
      <div v-for="role in customRoles" :key="role.id" class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl p-6 shadow-lg shadow-black/10 hover:border-indigo-500/20 transition-colors">
          <div class="flex flex-wrap items-center justify-between gap-2 mb-4">
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span class="w-5 h-5 rounded-full" :style="{ backgroundColor: role.color_hex }"></span>
              <h3 class="text-xl font-bold text-white break-all">{{ role.name }}</h3>
              <span v-if="role.rank === 100 || role.name === 'owner'" class="px-2 py-1 text-xs font-semibold rounded-full bg-red-500/15 border border-red-500/30 text-red-300">Rank 100 · Immutable</span>
              <span v-else class="px-2 py-1 text-xs font-semibold rounded-full bg-zinc-700/40 border border-white/10 text-zinc-300">[ Rank: {{ role.rank ?? 10 }} ]</span>
            </div>
            <div class="flex space-x-2">
              <button v-if="canManageRole(role)" @click="openEditModal(role)" class="text-blue-400 hover:text-blue-300 transition-colors" title="Edit Role">
                <Edit class="w-5 h-5" />
              </button>
              <button v-if="canDeleteRole(role)" @click="deleteRole(role)" class="text-red-500 hover:text-red-700 transition-colors" title="Delete Role">
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
    <div v-if="showCreateModal" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="showCreateModal = false">
      <div class="bg-zinc-900/95 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-6 sm:p-8 w-[95%] sm:w-full max-w-2xl max-h-[85vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>Create New Custom Role<span class="font-mono text-indigo-400">]</span></h2>

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
              <label for="role_rank" class="block text-sm font-medium text-zinc-400 mb-1">Role Rank</label>
              <input v-model.number="newRole.rank" type="number" id="role_rank" min="1" :max="maxRank" required
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50">
              <p class="mt-1 text-xs text-zinc-500">Must be lower than your current rank ({{ actorRank }}). Higher rank = more authority.</p>
            </div>
            <div>
              <label class="block text-sm font-medium text-zinc-400 mb-2">Permissions</label>
              <div class="space-y-4 bg-white/5 border border-white/10 rounded-xl p-4">
                <div v-for="section in PERMISSION_SECTIONS" :key="section.title">
                  <p class="text-xs font-semibold uppercase tracking-wider text-zinc-500 mb-2">{{ section.title }}</p>
                  <div class="grid grid-cols-1 md:grid-cols-2 gap-2">
                    <label v-for="[perm, label] in section.perms" :key="perm" class="flex items-center space-x-3">
                      <input type="checkbox" v-model="newRole.permissions" :value="perm"
                             class="rounded bg-zinc-700 text-purple-500 focus:ring-purple-500">
                      <span class="text-sm text-zinc-300">{{ label }}</span>
                    </label>
                  </div>
                </div>
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
    <div v-if="editingRole" class="fixed inset-0 z-[999] flex items-center justify-center bg-black/70 backdrop-blur-md p-4" @click.self="editingRole = null">
      <div class="bg-zinc-900/95 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl p-6 sm:p-8 w-[95%] sm:w-full max-w-2xl max-h-[85vh] overflow-y-auto">
        <h2 class="text-2xl font-bold mb-6"><span class="font-mono text-indigo-400">[</span>Edit Role<span class="font-mono text-indigo-400">]</span>: {{ editingRole.name }}</h2>

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
              <label for="edit_role_rank" class="block text-sm font-medium text-zinc-400 mb-1">Role Rank</label>
              <input v-model.number="editForm.rank" type="number" id="edit_role_rank" min="1" :max="maxRank"
                     :disabled="isImmutableRole(editingRole)" required
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50 disabled:opacity-50 disabled:cursor-not-allowed">
              <p v-if="isImmutableRole(editingRole)" class="mt-1 text-xs text-zinc-500">The owner role is immutable and cannot be re-ranked.</p>
              <p v-else class="mt-1 text-xs text-zinc-500">Must be lower than your current rank ({{ actorRank }}). Higher rank = more authority.</p>
            </div>
            <div>
              <label class="block text-sm font-medium text-zinc-400 mb-2">Permissions</label>
              <div class="space-y-4 bg-white/5 border border-white/10 rounded-xl p-4">
                <div v-for="section in PERMISSION_SECTIONS" :key="section.title">
                  <p class="text-xs font-semibold uppercase tracking-wider text-zinc-500 mb-2">{{ section.title }}</p>
                  <div class="grid grid-cols-1 md:grid-cols-2 gap-2">
                    <label v-for="[perm, label] in section.perms" :key="perm" class="flex items-center space-x-3">
                      <input type="checkbox" v-model="editForm.permissions" :value="perm"
                             class="rounded bg-zinc-700 text-purple-500 focus:ring-purple-500">
                      <span class="text-sm text-zinc-300">{{ label }}</span>
                    </label>
                  </div>
                </div>
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
import { ref, computed, inject, onMounted } from 'vue';
import { Plus, Trash2, Shield, Edit, Server, Users, ScrollText, DatabaseBackup } from 'lucide-vue-next';

const ROLE_RANK = { owner: 100, admin: 80, client: 30, viewer: 10 };

// Built-in role names that may never be created, edited or deleted through the UI.
const SYSTEM_ROLE_NAMES = ['owner', 'admin', 'client', 'viewer'];

const PERMISSION_SECTIONS = [
  {
    title: 'Nodes',
    icon: 'Server',
    perms: [
      ['can_view_nodes', 'View Nodes'],
      ['can_switch_vpn', 'Switch VPN'],
      ['can_edit_sub', 'Manage Sub URL'],
      ['can_rename_node', 'Rename Nodes'],
      ['can_terminate_node', 'Terminate & Self-Destruct'],
      ['can_view_node_logs', 'View Node Logs'],
      ['can_update_client', 'Push Client Files (OTA)'],
      ['can_purge_nodes', 'Purge Offline Nodes'],
    ],
  },
  {
    title: 'Users',
    icon: 'Users',
    perms: [
      ['can_view_users', 'View Fleet Users'],
      ['can_create_users', 'Add New Users'],
      ['can_edit_users', 'Edit Users'],
      ['can_delete_users', 'Delete Users'],
    ],
  },
  {
    title: 'Roles',
    icon: 'Shield',
    perms: [
      ['can_view_roles', 'View Roles & Permissions'],
      ['can_manage_roles', 'Create / Edit / Delete Roles'],
    ],
  },
  {
    title: 'Logs & Backups',
    icon: 'ScrollText',
    perms: [
      ['can_view_audit_logs', 'View Audit Trail'],
      ['can_view_master_logs', 'View Master Server Logs'],
      ['can_export_backups', 'Export Backups (ZIP)'],
    ],
  },
];

const PERMISSION_LABELS = Object.fromEntries(PERMISSION_SECTIONS.flatMap(s => s.perms));
const ALL_PERMS = Object.keys(PERMISSION_LABELS);

export default {
  name: 'RoleManager',
  components: { Plus, Trash2, Shield, Edit, Server, Users, ScrollText, DatabaseBackup },
  setup() {
    const authCtx = inject('authCtx', {});
    const actorRole = computed(() => authCtx.user?.value?.role || '');
    const canManageRoles = computed(() => authCtx.canManageRoles?.value ?? false);

    const customRoles = ref([]);
    const showCreateModal = ref(false);
    const editingRole = ref(null);
    const newRole = ref({ name: '', color_hex: '#FF5733', rank: 10, permissions: [] });
    const editForm = ref({ name: '', color_hex: '#FF5733', rank: 10, permissions: [] });

    // Actor's effective rank: prefer the DB-stored rank for the actor's role
    // if available, falling back to the built-in table.
    const actorRank = computed(() => {
      const name = actorRole.value;
      const match = (customRoles.value || []).find(r => r.name === name);
      if (match && match.rank) return match.rank;
      return ROLE_RANK[name] ?? 10;
    });
    const maxRank = computed(() => Math.max(1, actorRank.value - 1));

    const isImmutableRole = (role) => role && (role.rank === 100 || role.name === 'owner');

    const canManageRole = (role) => {
      if (!canManageRoles.value) return false;
      if (isImmutableRole(role)) return false;
      return (role.rank ?? ROLE_RANK[role.name] ?? 10) < actorRank.value;
    };

    const canDeleteRole = (role) => {
      if (!canManageRoles.value) return false;
      if (isImmutableRole(role)) return false;
      if (SYSTEM_ROLE_NAMES.includes(role.name)) return false;
      return (role.rank ?? ROLE_RANK[role.name] ?? 10) < actorRank.value;
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
          rank: newRole.value.rank,
          permissions_json: JSON.stringify(permsObj),
        };
        const response = await fetch('/api/web/roles', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(roleData),
        });
        if (!response.ok) {
          let errMsg = 'Failed to create role';
          try {
            const errData = await response.json();
            if (errData.error) errMsg = errData.error;
          } catch (_) {}
          throw new Error(errMsg);
        }

        newRole.value = { name: '', color_hex: '#FF5733', rank: 10, permissions: [] };
        showCreateModal.value = false;
        await fetchCustomRoles();
        alert('Custom role created successfully!');
      } catch (error) {
        console.error('Error creating role:', error);
        alert(error.message || 'Could not create role.');
      }
    };

    const openEditModal = (role) => {
      editingRole.value = role;
      editForm.value = {
        id: role.id,
        name: role.name,
        color_hex: role.color_hex,
        rank: role.rank ?? ROLE_RANK[role.name] ?? 10,
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
          rank: editForm.value.rank,
          permissions_json: JSON.stringify(permsObj),
        };
        const response = await fetch(`/api/web/roles/${editForm.value.id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(roleData),
        });
        if (!response.ok) {
          let errMsg = 'Failed to update role';
          try {
            const errData = await response.json();
            if (errData.error) errMsg = errData.error;
          } catch (_) {}
          throw new Error(errMsg);
        }

        editingRole.value = null;
        await fetchCustomRoles();
        alert('Custom role updated successfully!');
      } catch (error) {
        console.error('Error updating role:', error);
        alert(error.message || 'Could not update role.');
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
      PERMISSION_SECTIONS,
      actorRank,
      maxRank,
      isImmutableRole,
      canManageRole,
      canDeleteRole,
      handleCreateRole,
      openEditModal,
      handleEditRole,
      deleteRole,
    };
  },
};
</script>
