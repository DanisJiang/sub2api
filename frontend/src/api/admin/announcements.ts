/**
 * Admin Announcements API endpoints
 * Handles announcement management for administrators
 */

import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

/**
 * Announcement interface
 */
export interface Announcement {
  id: number
  title: string
  content: string
  enabled: boolean
  priority: number
  created_at: string
  updated_at: string
}

/**
 * Create announcement request
 */
export interface CreateAnnouncementRequest {
  title: string
  content: string
  enabled: boolean
  priority: number
}

/**
 * Update announcement request
 */
export interface UpdateAnnouncementRequest {
  title?: string
  content?: string
  enabled?: boolean
  priority?: number
}

/**
 * List all announcements with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @returns Paginated list of announcements
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  options?: {
    signal?: AbortSignal
  }
): Promise<PaginatedResponse<Announcement>> {
  const { data } = await apiClient.get<PaginatedResponse<Announcement>>('/admin/announcements', {
    params: {
      page,
      page_size: pageSize
    },
    signal: options?.signal
  })
  return data
}

/**
 * Get announcement by ID
 * @param id - Announcement ID
 * @returns Announcement details
 */
export async function getById(id: number): Promise<Announcement> {
  const { data } = await apiClient.get<Announcement>(`/admin/announcements/${id}`)
  return data
}

/**
 * Create a new announcement
 * @param announcement - Announcement data
 * @returns Created announcement
 */
export async function create(announcement: CreateAnnouncementRequest): Promise<Announcement> {
  const { data } = await apiClient.post<Announcement>('/admin/announcements', announcement)
  return data
}

/**
 * Update an existing announcement
 * @param id - Announcement ID
 * @param announcement - Updated announcement data
 * @returns Updated announcement
 */
export async function update(id: number, announcement: UpdateAnnouncementRequest): Promise<Announcement> {
  const { data } = await apiClient.put<Announcement>(`/admin/announcements/${id}`, announcement)
  return data
}

/**
 * Delete an announcement
 * @param id - Announcement ID
 * @returns Success confirmation
 */
export async function deleteAnnouncement(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/announcements/${id}`)
  return data
}

export const announcementsAPI = {
  list,
  getById,
  create,
  update,
  delete: deleteAnnouncement
}

export default announcementsAPI
