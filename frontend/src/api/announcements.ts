/**
 * Public Announcements API
 * Fetches enabled announcements for display to all users
 */

import { apiClient } from './client'

/**
 * Public announcement interface (subset of admin announcement)
 */
export interface PublicAnnouncement {
  id: number
  title: string
  content: string
  priority: number
  created_at: string
}

/**
 * Get all enabled announcements
 * @returns Array of enabled announcements sorted by priority
 */
export async function getAnnouncements(): Promise<PublicAnnouncement[]> {
  const { data } = await apiClient.get<PublicAnnouncement[]>('/announcements')
  return data
}

export const announcementsAPI = {
  getAnnouncements
}

export default announcementsAPI
