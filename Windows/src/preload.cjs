'use strict';

const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('ksfAssistant', Object.freeze({
  dashboard: (forceAccountRefresh = false) => ipcRenderer.invoke('dashboard:read', forceAccountRefresh === true),
  tokenHistory: (options = { dayCount: 30 }) => ipcRenderer.invoke('token-history:read', options),
  pricingCatalog: () => ipcRenderer.invoke('pricing-catalog:read'),
  settings: () => ipcRenderer.invoke('settings:read'),
  updateSettings: (patch) => ipcRenderer.invoke('settings:update', patch),
  openExternal: (url) => ipcRenderer.invoke('external:open', url),
  chooseDirectory: (kind) => ipcRenderer.invoke('directory:choose', kind),
  setPinned: (projectId, pinned) => ipcRenderer.invoke('project:set-pinned', { projectId, pinned }),
  openPath: (targetPath) => ipcRenderer.invoke('path:open', targetPath),
  openTask: (threadId) => ipcRenderer.invoke('task:open', threadId),
  createTask: (projectId, purpose) => ipcRenderer.invoke('task:create', { projectId, purpose }),
  launchProject: (projectId) => ipcRenderer.invoke('project:launch', projectId),
  createTaskLink: (payload) => ipcRenderer.invoke('feishu:task-link-create', payload),
  releaseTaskLink: (payload) => ipcRenderer.invoke('feishu:task-link-release', payload),
  interruptTaskLink: (payload) => ipcRenderer.invoke('feishu:task-link-interrupt', payload),
  readFeishuConfiguration: (options = { refresh: false }) => ipcRenderer.invoke('feishu:configuration-read', options),
  actFeishuConfiguration: (payload) => ipcRenderer.invoke('feishu:configuration-action', payload),
  toolchainStatus: () => ipcRenderer.invoke('toolchain:status'),
  installToolchain: (confirm) => ipcRenderer.invoke('toolchain:install', confirm),
  openFeishuFlow: (payload) => ipcRenderer.invoke('feishu:flow-open', payload),
  resize: (height) => ipcRenderer.send('window:resize', height),
  hide: () => ipcRenderer.send('window:hide'),
  quit: () => ipcRenderer.send('app:quit'),
}));
