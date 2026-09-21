/**
 * System API endpoints for admin operations
 *
 * This fork removed the upstream binary self-update/rollback endpoints
 * (checkUpdates/performUpdate/getRollbackVersions/rollback) — this fork is
 * build-from-source only, and updates happen via `git pull` + rebuild, never
 * by downloading a prebuilt binary. See FORK_MAINTENANCE.md.
 */

import { apiClient } from '../client'

export interface VersionInfo {
  version: string
  security_warnings: string[]
}

/**
 * Get current version and active insecure-config warnings
 */
export async function getVersion(): Promise<VersionInfo> {
  const { data } = await apiClient.get<VersionInfo>('/admin/system/version')
  return data
}

/**
 * Restart the service (e.g. after a rebuild)
 */
export async function restartService(): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>('/admin/system/restart')
  return data
}

export const systemAPI = {
  getVersion,
  restartService
}

export default systemAPI
