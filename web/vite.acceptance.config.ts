import path from 'node:path'
import { fileURLToPath } from 'node:url'

import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const rootDir = path.dirname(fileURLToPath(import.meta.url))
const apiProxyTarget = requiredLoopbackURL('VITE_API_PROXY_TARGET')
const outputDirectory = requiredBuildDirectory('LLMGATEWAY_REAL_WEB_DIST')
const proxy = {
  '/api': apiProxyTarget,
  '/v1': apiProxyTarget,
}

export default defineConfig({
  envDir: false,
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(rootDir, 'src'),
    },
  },
  build: {
    outDir: outputDirectory,
    emptyOutDir: true,
    sourcemap: true,
  },
  server: {
    host: '127.0.0.1',
    strictPort: true,
    proxy,
  },
  preview: {
    host: '127.0.0.1',
    strictPort: true,
    proxy,
  },
})

function requiredBuildDirectory(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required`)
  const buildRoot = path.resolve(rootDir, '..', '.build')
  const output = path.resolve(value)
  const relative = path.relative(buildRoot, output)
  if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) {
    throw new Error(`${name} must be a child of the repository .build directory`)
  }
  return output
}

function requiredLoopbackURL(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required`)
  const parsed = new URL(value)
  if (parsed.protocol !== 'http:' || parsed.hostname !== '127.0.0.1') {
    throw new Error(`${name} must be a loopback HTTP URL`)
  }
  return parsed.origin
}
