'use strict';

const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('usageBar', Object.freeze({
  dashboard: () => ipcRenderer.invoke('dashboard:read'),
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
  sendFeishuTest: (targetAlias) => ipcRenderer.invoke('feishu:test', targetAlias),
  setFeishuProfile: (profile) => ipcRenderer.invoke('feishu:profile-set', profile),
  controlFeishuService: (action) => ipcRenderer.invoke('feishu:service-control', action),
  configureFeishu: (credential) => ipcRenderer.invoke('feishu:auth-configure', credential),
  startFeishuAuth: () => ipcRenderer.invoke('feishu:auth-start'),
  finishFeishuAuth: () => ipcRenderer.invoke('feishu:auth-finish'),
  readFeishuPermissions: () => ipcRenderer.invoke('feishu:permissions-read'),
  resize: (height) => ipcRenderer.send('window:resize', height),
  hide: () => ipcRenderer.send('window:hide'),
  quit: () => ipcRenderer.send('app:quit'),
}));
