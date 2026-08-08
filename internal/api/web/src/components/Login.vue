<template>
  <div class="flex items-center justify-center min-h-screen bg-[#09090b]">
    <div class="w-full max-w-sm p-8 space-y-8 bg-zinc-900/95 md:bg-zinc-900/60 md:backdrop-blur-2xl rounded-3xl shadow-2xl border border-white/10">
      <div class="text-center">
        <h2 class="text-2xl font-semibold text-zinc-100 tracking-tight">{{ t('login_title') }}</h2>
        <p class="mt-2 text-sm text-zinc-500">{{ t('login_subtitle') }}</p>
      </div>
      <form class="space-y-6" @submit.prevent="handleLogin">
        <div class="relative">
          <input v-model="username" type="text" :placeholder="t('login_username')" class="w-full px-4 py-3 bg-zinc-800/60 border border-white/10 rounded-xl focus:ring-2 focus:ring-zinc-500/40 focus:border-zinc-500/40 focus:outline-none placeholder-zinc-500" />
        </div>
        <div class="relative">
          <input v-model="password" type="password" :placeholder="t('login_password')" class="w-full px-4 py-3 bg-zinc-800/60 border border-white/10 rounded-xl focus:ring-2 focus:ring-zinc-500/40 focus:border-zinc-500/40 focus:outline-none placeholder-zinc-500" />
        </div>
        <div v-if="error" class="text-red-400 text-sm text-center">
          {{ error }}
        </div>
        <div>
          <button type="submit" class="w-full px-4 py-2.5 font-semibold text-white bg-indigo-600 hover:bg-indigo-500 rounded-xl shadow-lg shadow-indigo-900/50 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-[#09090b] focus:ring-indigo-500/50 transition-colors">
            {{ t('login_button') }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<script>
import { ref, inject } from 'vue';
import axios from 'axios';

export default {
  name: 'Login',
  emits: ['authenticated'],
  setup(_, { emit }) {
    const t = inject('t') || ((k) => k);
    const username = ref('');
    const password = ref('');
    const error = ref(null);

    const handleLogin = async () => {
      error.value = null;
      try {
        const response = await axios.post('/api/auth/login', {
          username: username.value,
          password: password.value,
        });

        if (response.data.status === 'ok') {
          emit('authenticated');
        } else {
          throw new Error(response.data.message || 'Login failed');
        }
      } catch (e) {
        if (e.response?.status === 401) {
          error.value = t('login_invalid');
        } else if (e.response?.status === 429) {
          error.value = t('login_rate');
        } else if (e.response?.status === 403) {
          error.value = t('login_denied');
        } else {
          const detail = e.response?.data;
          const message = typeof detail === 'string' ? detail : detail?.message;
          error.value = message || e.message || t('login_unknown');
        }
      }
    };

    return {
      t,
      username,
      password,
      error,
      handleLogin,
    };
  },
};
</script>
