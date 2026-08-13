import { api } from './client'
import type { Settings, SettingsInput } from '../types'

export function login(password: string): Promise<{ ok: true }> {
  return api.post('/api/auth/login', { password })
}

export function logout(): Promise<{ ok: true }> {
  return api.post('/api/auth/logout')
}

export function getSettings(): Promise<Settings> {
  return api.get('/api/settings')
}

export function updateSettings(input: SettingsInput): Promise<{ ok: true }> {
  return api.put('/api/settings', input)
}
