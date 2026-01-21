import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import fs from 'fs'

// https://vite.dev/config/
const httpsKeyPath = process.env.VITE_HTTPS_KEY
const httpsCertPath = process.env.VITE_HTTPS_CERT
const hasHttps = Boolean(httpsKeyPath && httpsCertPath)
const serverPort = Number(process.env.VITE_PORT ?? (hasHttps ? 443 : 5173))
const serverHost = process.env.VITE_HOST ?? '0.0.0.0'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    host: serverHost,
    port: serverPort,
    https: hasHttps
      ? {
          key: fs.readFileSync(path.resolve(httpsKeyPath!)),
          cert: fs.readFileSync(path.resolve(httpsCertPath!)),
        }
      : false,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: false,
      },
    },
  },
})
