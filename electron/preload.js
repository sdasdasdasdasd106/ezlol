'use strict'
const { contextBridge, ipcRenderer } = require('electron')
contextBridge.exposeInMainWorld('ezlol', {
  platform: process.platform,
  electron: true,
  notify: (title, body) => ipcRenderer.invoke('notify', String(title), String(body)),
  onUpdate: (fn) => ipcRenderer.on('update-state', (_e, st) => fn(st)),
  installUpdate: () => ipcRenderer.invoke('install-update'),
  checkUpdate: () => ipcRenderer.invoke('check-update'),
  // Desktop settings (login item, tray, voice, hotkeys) live in the main process.
  getAppSettings: () => ipcRenderer.invoke('app-settings:get'),
  setAppSettings: (patch) => ipcRenderer.invoke('app-settings:set', patch && typeof patch === 'object' ? patch : {}),
  onAppSettings: (fn) => ipcRenderer.on('app-settings', (_e, s) => fn(s)),
  // Global hotkeys are let go while Settings records a new one.
  suspendHotkeys: (on) => ipcRenderer.invoke('hotkeys:suspend', !!on),
  resetHotkeys: () => ipcRenderer.invoke('hotkeys:defaults'),
  testVoice: () => ipcRenderer.invoke('voice:test'),
  // A hotkey changed the game state (scan, pick): poll now instead of waiting.
  onGameRefresh: (fn) => ipcRenderer.on('game:refresh', () => fn()),
  // What a hotkey did, to show (and speak when say is set).
  onHotkeyResult: (fn) => ipcRenderer.on('hotkey:result', (_e, text, say) => fn(String(text), !!say)),
  onSay: (fn) => ipcRenderer.on('voice:say', (_e, text, force) => fn(String(text), !!force)),
})
