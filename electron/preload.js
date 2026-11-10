'use strict'
const { contextBridge, ipcRenderer } = require('electron')
contextBridge.exposeInMainWorld('ezlol', {
  platform: process.platform,
  electron: true,
  notify: (title, body) => ipcRenderer.invoke('notify', String(title), String(body)),
  onUpdate: (fn) => ipcRenderer.on('update-state', (_e, st) => fn(st)),
  installUpdate: () => ipcRenderer.invoke('install-update'),
  checkUpdate: () => ipcRenderer.invoke('check-update'),
  // Desktop settings (login item, tray) live in the main process.
  getAppSettings: () => ipcRenderer.invoke('app-settings:get'),
  setAppSettings: (patch) => ipcRenderer.invoke('app-settings:set', patch && typeof patch === 'object' ? patch : {}),
  onAppSettings: (fn) => ipcRenderer.on('app-settings', (_e, s) => fn(s)),
  previewOverlay: () => ipcRenderer.invoke('overlay:preview'),
  // Used by the always-on-top overlay window only.
  overlay: {
    want: (state, height) => ipcRenderer.send('overlay:want', String(state), Number(height) || 0),
    onMode: (fn) => ipcRenderer.on('overlay:mode', (_e, m) => fn(m)),
    onHotkeys: (fn) => ipcRenderer.on('overlay:hotkeys', (_e, h) => fn(h)),
    onFlash: (fn) => ipcRenderer.on('overlay:flash', (_e, t) => fn(String(t))),
    onPreview: (fn) => ipcRenderer.on('overlay:preview', () => fn()),
  },
})
