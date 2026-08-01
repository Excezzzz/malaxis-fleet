<template>
  <div class="flex items-center justify-center min-h-screen bg-gray-900">
    <div class="w-full max-w-md p-8 space-y-8 bg-gray-950 rounded-2xl shadow-lg border border-gray-800">
      <div class="text-center">
        <h2 class="text-4xl font-bold text-white">Welcome Back</h2>
        <p class="mt-2 text-gray-400">Sign in to manage your fleet</p>
      </div>
      <form class="space-y-6" @submit.prevent="handleLogin">
        <div class="relative">
          <input v-model="username" type="text" placeholder="Username" class="w-full px-4 py-3 bg-gray-800 border border-gray-700 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:outline-none" />
        </div>
        <div class="relative">
          <input v-model="password" type="password" placeholder="Password" class="w-full px-4 py-3 bg-gray-800 border border-gray-700 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:outline-none" />
        </div>
        <div v-if="error" class="text-red-400 text-sm text-center">
          {{ error }}
        </div>
        <div>
          <button type="submit" class="w-full px-4 py-3 font-semibold text-white bg-indigo-600 rounded-lg hover:bg-indigo-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-gray-900 focus:ring-indigo-500 transition-all duration-200">
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

export default {
  name: 'Login',
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
        error.value = e.response?.data?.message || e.message || 'An unknown error occurred.';
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
