const path = require('path')
const fs = require('fs')
const { defineConfig } = require('@vue/cli-service')

// Inject the root VERSION file (single source of truth) as VUE_APP_VERSION.
let appVersion = 'dev'
try {
  appVersion = fs.readFileSync(path.resolve(__dirname, '../../../VERSION'), 'utf8').trim()
} catch (e) {
  console.warn('[vue.config] VERSION file not found, falling back to "dev"')
}
process.env.VUE_APP_VERSION = appVersion

module.exports = defineConfig({
  lintOnSave: false,
  devServer: {
    proxy: {
      '/api': {
        target: 'http://localhost:8000',
        changeOrigin: true,
      },
    },
  },
})
