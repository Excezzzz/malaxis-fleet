<template>
  <div>
    <div class="flex flex-wrap justify-between items-center gap-3 mb-8">
      <h1 class="text-4xl font-bold tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ t('roles_title') }}<span class="font-mono text-indigo-400">]</span></h1>
      <button v-if="canManageRoles" @click="showCreateModal = true" class="flex items-center space-x-2 px-4 py-2 bg-purple-500/15 hover:bg-purple-500/25 border border-purple-500/30 text-purple-100 rounded-xl transition-colors">
        <Plus class="w-5 h-5" />
        <span class="font-mono text-sm truncate min-w-0">[{{ t('add_role') }}]</span>
      </button>
    </div>

    <!-- Existing Roles -->
    <div v-if="customRoles.length > 0" class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6">
      <div v-for="role in sortedRoles" :key="role.id" class="bg-zinc-900/40 backdrop-blur-md border border-white/5 rounded-2xl p-6 shadow-lg shadow-black/10 hover:border-indigo-500/20 transition-colors">
          <div class="flex flex-wrap items-center justify-between gap-2 mb-4">
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 min-w-0">
              <span class="w-5 h-5 rounded-full shrink-0" :style="{ backgroundColor: role.color_hex }"></span>
              <h3 class="text-xl font-bold text-white break-all min-w-0">{{ role.name }}</h3>
              <span v-if="role.rank === 100 || role.name === 'owner'" class="px-2 py-1 text-xs font-semibold rounded-full bg-red-500/15 border border-red-500/30 text-red-300">{{ t('roles_rank_immutable') }}</span>
              <span v-else class="px-2 py-1 text-xs font-semibold rounded-full bg-zinc-700/40 border border-white/10 text-zinc-300">[ {{ t('roles_rank') }}: {{ role.rank ?? 10 }} ]</span>
            </div>
            <div class="flex space-x-2 shrink-0">
              <button v-if="canManageRole(role)" @click="openEditModal(role)" class="text-blue-400 hover:text-blue-300 transition-colors" :title="t('roles_edit_tt')">
                <Edit class="w-5 h-5" />
              </button>
              <button v-if="canDeleteRole(role)" @click="deleteRole(role)" class="text-red-500 hover:text-red-700 transition-colors" :title="t('roles_delete_tt')">
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
          <p class="text-xs uppercase tracking-wider font-medium text-zinc-500">{{ t('roles_permissions') }}:</p>
          <div v-if="parsePermissions(role.permissions_json).length > 0" class="flex flex-wrap gap-2">
            <span v-for="perm in parsePermissions(role.permissions_json)" :key="perm" class="px-2 py-1 bg-white/5 border border-white/10 text-xs text-zinc-300 rounded">
              {{ permLabel(perm) }}
            </span>
          </div>
          <p v-else class="text-sm text-zinc-500">{{ t('roles_no_perms') }}</p>
        </div>

        <div class="mt-4 text-xs text-zinc-500">
          {{ t('roles_created') }}: {{ new Date(role.created_at).toLocaleDateString() }}
        </div>
      </div>
    </div>

    <div v-else class="text-center py-16">
      <Shield class="w-16 h-16 text-zinc-600 mx-auto mb-4" />
      <p class="text-zinc-500 text-lg">{{ t('roles_empty') }}</p>
      <p class="text-zinc-600 text-sm mt-2">{{ t('roles_empty_hint') }}</p>
    </div>

    <!-- Create Role Modal -->
    <div v-if="showCreateModal" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="showCreateModal = false">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-6 tracking-tight"><span class="font-mono text-indigo-400">[</span>{{ editingRole ? t('edit_role') : t('add_role') }}<span class="font-mono text-indigo-400">]</span></h2>

        <form @submit.prevent="handleCreateRole">
          <div class="space-y-6">
            <div>
              <label for="role_name" class="block text-sm font-medium text-zinc-400 mb-1">{{ t('roles_name') }}</label>
              <input v-model="newRole.name" type="text" id="role_name" :placeholder="t('roles_name_ph')"
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50" required>
            </div>
            <div>
              <label for="role_color" class="block text-sm font-medium text-zinc-400 mb-1">{{ t('roles_color') }}</label>
              <div class="flex items-center space-x-3 mt-1">
                <input v-model="newRole.color_hex" type="color" id="role_color_picker"
                       class="h-10 w-16 rounded cursor-pointer bg-zinc-800 border-white/10">
                <input v-model="newRole.color_hex" type="text" id="role_color_hex" placeholder="#FF5733"
                       class="flex-1 bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50">
              </div>
            </div>
            <div>
              <label for="role_rank" class="block text-sm font-medium text-zinc-400 mb-1">{{ t('roles_rank_label') }}</label>
              <input v-model.number="newRole.rank" type="number" id="role_rank" min="1" :max="maxRank" required
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50">
              <p class="mt-1 text-xs text-zinc-500">{{ t('roles_rank_label_hint', { rank: actorRank }) }}</p>
            </div>
            <div>
              <label class="block text-sm font-medium text-zinc-400 mb-2">{{ t('roles_perms_label') }}</label>
              <div class="space-y-4 bg-white/5 border border-white/10 rounded-xl p-4">
                <div v-for="section in PERMISSION_SECTIONS" :key="section.titleKey">
                  <p class="text-xs font-semibold uppercase tracking-wider text-zinc-500 mb-2">{{ t(section.titleKey) }}</p>
                  <div class="grid grid-cols-1 md:grid-cols-2 gap-x-4 gap-y-2">
                    <label v-for="[perm, label] in section.perms" :key="perm" class="flex items-center gap-3 cursor-pointer group py-1">
                      <input type="checkbox" v-model="newRole.permissions" :value="perm" class="sr-only peer" />
                      <div class="w-6 h-6 rounded-md border border-white/10 bg-black/40 peer-checked:bg-indigo-500 peer-checked:border-indigo-400 flex items-center justify-center transition-all shrink-0">
                        <svg class="w-4 h-4 text-white opacity-0 peer-checked:opacity-100 transition-opacity" viewBox="0 0 24 24" fill="none"><path d="M5 13l4 4L19 7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
                      </div>
                      <span class="text-sm text-zinc-300 group-hover:text-white transition-colors min-w-0">{{ t(label) }}</span>
                    </label>
                  </div>
                </div>
              </div>
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="showCreateModal = false"
                    class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
            <button type="submit"
                    class="px-4 py-2 bg-purple-500/20 hover:bg-purple-500/30 border border-purple-500/30 text-purple-100 rounded-xl transition-colors">{{ t('roles_create_btn') }}</button>
          </div>
        </form>
      </div>
    </div>

    <!-- Edit Role Modal -->
    <div v-if="editingRole" :class="['fixed inset-0 z-[999] flex items-center justify-center p-4', modalBackdrop]" style="backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);" @click.self="editingRole = null">
      <div class="bg-zinc-900/90 backdrop-blur-xl rounded-3xl border border-white/10 p-6 w-[95%] sm:w-full max-w-lg max-h-[85vh] overflow-y-auto shadow-2xl">
        <h2 class="text-2xl font-bold mb-6"><span class="font-mono text-indigo-400">[</span>{{ t('edit_role') }}<span class="font-mono text-indigo-400">]</span>: {{ editingRole.name }}</h2>

        <form @submit.prevent="handleEditRole">
          <div class="space-y-6">
            <div>
              <label for="edit_role_name" class="block text-sm font-medium text-zinc-400 mb-1">{{ t('roles_name') }}</label>
              <input v-model="editForm.name" type="text" id="edit_role_name"
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50" required>
            </div>
            <div>
              <label for="edit_role_color" class="block text-sm font-medium text-zinc-400 mb-1">{{ t('roles_color') }}</label>
              <div class="flex items-center space-x-3 mt-1">
                <input v-model="editForm.color_hex" type="color" id="edit_role_color_picker"
                       class="h-10 w-16 rounded cursor-pointer bg-zinc-800 border-white/10">
                <input v-model="editForm.color_hex" type="text" id="edit_role_color_hex" placeholder="#FF5733"
                       class="flex-1 bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50">
              </div>
            </div>
            <div>
              <label for="edit_role_rank" class="block text-sm font-medium text-zinc-400 mb-1">{{ t('roles_rank_label') }}</label>
              <input v-model.number="editForm.rank" type="number" id="edit_role_rank" min="1" :max="maxRank"
                     :disabled="isImmutableRole(editingRole)" required
                     class="mt-1 block w-full bg-zinc-800 border-white/10 rounded-xl shadow-sm py-2 px-3 text-white focus:outline-none focus:ring-purple-500 focus:border-purple-500/50 disabled:opacity-50 disabled:cursor-not-allowed">
              <p v-if="isImmutableRole(editingRole)" class="mt-1 text-xs text-zinc-500">{{ t('roles_immutable_hint') }}</p>
              <p v-else class="mt-1 text-xs text-zinc-500">{{ t('roles_rank_label_hint', { rank: actorRank }) }}</p>
            </div>
            <div>
              <label class="block text-sm font-medium text-zinc-400 mb-2">{{ t('roles_perms_label') }}</label>
              <div class="space-y-4 bg-white/5 border border-white/10 rounded-xl p-4">
                <div v-for="section in PERMISSION_SECTIONS" :key="section.titleKey">
                  <p class="text-xs font-semibold uppercase tracking-wider text-zinc-500 mb-2">{{ t(section.titleKey) }}</p>
                  <div class="grid grid-cols-1 md:grid-cols-2 gap-x-4 gap-y-2">
                    <label v-for="[perm, label] in section.perms" :key="perm" class="flex items-center gap-3 cursor-pointer group py-1">
                      <input type="checkbox" v-model="editForm.permissions" :value="perm" class="sr-only peer" />
                      <div class="w-6 h-6 rounded-md border border-white/10 bg-black/40 peer-checked:bg-indigo-500 peer-checked:border-indigo-400 flex items-center justify-center transition-all shrink-0">
                        <svg class="w-4 h-4 text-white opacity-0 peer-checked:opacity-100 transition-opacity" viewBox="0 0 24 24" fill="none"><path d="M5 13l4 4L19 7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
                      </div>
                      <span class="text-sm text-zinc-300 group-hover:text-white transition-colors min-w-0">{{ t(label) }}</span>
                    </label>
                  </div>
                </div>
              </div>
            </div>
          </div>
          <div class="mt-8 flex justify-end space-x-4">
            <button type="button" @click="editingRole = null"
                    class="px-4 py-2 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-colors">{{ t('cancel') }}</button>
            <button type="submit"
                    class="px-4 py-2 bg-purple-500/20 hover:bg-purple-500/30 border border-purple-500/30 text-purple-100 rounded-xl transition-colors">{{ t('save') }}</button>
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

const PERMISSION_SECTIONS = [
  {
    titleKey: 'roles_sec_nodes',
    icon: 'Server',
    perms: [
      ['can_view_nodes', 'perm_view_nodes'],
      ['can_switch_vpn', 'perm_switch_vpn'],
      ['can_edit_sub', 'perm_edit_sub'],
      ['can_rename_node', 'perm_rename_node'],
      ['can_terminate_node', 'perm_terminate_node'],
      ['can_view_node_logs', 'perm_view_node_logs'],
      ['can_update_client', 'perm_update_client'],
      ['can_purge_nodes', 'perm_purge_nodes'],
    ],
  },
  {
    titleKey: 'roles_sec_users',
    icon: 'Users',
    perms: [
      ['can_view_users', 'perm_view_users'],
      ['can_create_users', 'perm_create_users'],
      ['can_edit_users', 'perm_edit_users'],
      ['can_delete_users', 'perm_delete_users'],
    ],
  },
  {
    titleKey: 'roles_sec_roles',
    icon: 'Shield',
    perms: [
      ['can_view_roles', 'perm_view_roles'],
      ['can_manage_roles', 'perm_manage_roles'],
    ],
  },
  {
    titleKey: 'roles_sec_logs',
    icon: 'ScrollText',
    perms: [
      ['can_view_audit_logs', 'perm_view_audit_logs'],
      ['can_view_master_logs', 'perm_view_master_logs'],
      ['can_export_backups', 'perm_export_backups'],
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
    const t = inject('t') || ((k) => k);
    const prefs = inject('prefs', ref({ theme_mode: 'obsidian' }));
    const modalBackdrop = computed(() => prefs.value.theme_mode === 'light' ? 'bg-zinc-900/25 backdrop-blur-sm' : 'bg-black/75 backdrop-blur-md');
    const actorRole = computed(() => authCtx.user?.value?.role || '');
    const canManageRoles = computed(() => authCtx.canManageRoles?.value ?? false);

    const customRoles = ref([]);
    const showCreateModal = ref(false);
    const editingRole = ref(null);
    const newRole = ref({ name: '', color_hex: '#FF5733', rank: 10, permissions: [] });
    const editForm = ref({ name: '', color_hex: '#FF5733', rank: 10, permissions: [] });

    const roleEffectiveRank = (role) => role.rank ?? ROLE_RANK[role.name] ?? 10;

    // Roles are rendered sorted by rank descending (Owner 100 -> Admin 80 ->
    // Viewer 10), then alphabetically by name within equal ranks.
    const sortedRoles = computed(() => {
      return [...customRoles.value].sort((a, b) => {
        const rankDiff = roleEffectiveRank(b) - roleEffectiveRank(a);
        if (rankDiff !== 0) return rankDiff;
        return String(a.name).localeCompare(String(b.name));
      });
    });

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

    const permLabel = (perm) => t(PERMISSION_LABELS[perm] || perm);

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
          let errMsg = t('roles_create_failed');
          try {
            const errData = await response.json();
            if (errData.error) errMsg = errData.error;
          } catch (_) {}
          throw new Error(errMsg);
        }

        newRole.value = { name: '', color_hex: '#FF5733', rank: 10, permissions: [] };
        showCreateModal.value = false;
        await fetchCustomRoles();
        alert(t('roles_created_ok'));
      } catch (error) {
        console.error('Error creating role:', error);
        alert(error.message || t('roles_create_failed'));
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
          let errMsg = t('roles_update_failed');
          try {
            const errData = await response.json();
            if (errData.error) errMsg = errData.error;
          } catch (_) {}
          throw new Error(errMsg);
        }

        editingRole.value = null;
        await fetchCustomRoles();
        alert(t('roles_updated_ok'));
      } catch (error) {
        console.error('Error updating role:', error);
        alert(error.message || t('roles_update_failed'));
      }
    };

    const deleteRole = async (role) => {
      if (!confirm(t('roles_delete_confirm', { name: role.name }))) return;
      try {
        const response = await fetch(`/api/web/roles/${role.id}`, {
          method: 'DELETE',
        });
        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || t('roles_delete_failed'));
        }
        await fetchCustomRoles();
      } catch (error) {
        console.error('Error deleting role:', error);
        alert(error.message || t('roles_delete_failed'));
      }
    };

    onMounted(() => {
      fetchCustomRoles();
    });

    return {
      customRoles,
      sortedRoles,
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
      t,
    };
  },
};
</script>
