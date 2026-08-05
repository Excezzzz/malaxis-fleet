<template>
  <div class="flex items-center justify-center min-h-screen bg-[#09090b]">
    <div class="w-full max-w-md p-8 space-y-8 bg-zinc-900/60 backdrop-blur-2xl rounded-3xl shadow-2xl border border-white/10">
      <div class="text-center">
        <div class="mx-auto mb-4 w-12 h-12 rounded-2xl bg-indigo-500/15 border border-indigo-400/20 flex items-center justify-center">
          <Globe class="w-6 h-6 text-indigo-300" />
        </div>
        <h2 class="text-3xl font-bold text-white tracking-tight"><span class="font-mono text-indigo-400">[</span>Welcome Back<span class="font-mono text-indigo-400">]</span></h2>
        <p class="mt-2 text-zinc-400">Sign in to manage your fleet</p>
      </div>
      <form class="space-y-6" @submit.prevent="handleLogin">
        <div class="relative">
          <input v-model="username" type="text" placeholder="Username" class="w-full px-4 py-3 bg-zinc-800/60 border border-white/10 rounded-xl focus:ring-2 focus:ring-indigo-500/50 focus:border-indigo-500/40 focus:outline-none placeholder-zinc-500" />
        </div>
        <div class="relative">
          <input v-model="password" type="password" placeholder="Password" class="w-full px-4 py-3 bg-zinc-800/60 border border-white/10 rounded-xl focus:ring-2 focus:ring-indigo-500/50 focus:border-indigo-500/40 focus:outline-none placeholder-zinc-500" />
        </div>
        <div v-if="error" class="text-red-400 text-sm text-center">
          {{ error }}
        </div>
        <div>
          <button type="submit" class="w-full px-4 py-3 font-semibold text-white bg-indigo-500/20 hover:bg-indigo-500/30 border border-indigo-500/30 rounded-xl focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-[#09090b] focus:ring-indigo-500/50 transition-all duration-200">
            Login
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<script>
import { ref } from 'vue';
import axios from 'axios';
import { Globe } from 'lucide-vue-next';

export default {
  name: 'Login',
  components: { Globe },
  emits: ['authenticated'],
  setup(_, { emit }) {
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
          error.value = 'Invalid username or password';
        } else if (e.response?.status === 429) {
          error.value = 'Too many attempts, please try again in a minute.';
        } else if (e.response?.status === 403) {
          error.value = 'Access denied.';
        } else {
          const detail = e.response?.data;
          const message = typeof detail === 'string' ? detail : detail?.message;
          error.value = message || e.message || 'An unknown error occurred.';
        }
      }
    };

    return {
      username,
      password,
      error,
      handleLogin,
    };
  },
};
</script>
