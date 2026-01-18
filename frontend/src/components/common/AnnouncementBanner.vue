<template>
  <div v-if="visibleAnnouncements.length > 0" class="space-y-2">
    <TransitionGroup name="announcement">
      <div
        v-for="announcement in visibleAnnouncements"
        :key="announcement.id"
        class="relative flex items-start gap-3 rounded-lg bg-primary-50 px-4 py-3 dark:bg-primary-900/20"
      >
        <div class="flex-shrink-0 pt-0.5">
          <svg class="h-5 w-5 text-primary-600 dark:text-primary-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M10.34 15.84c-.688-.06-1.386-.09-2.09-.09H7.5a4.5 4.5 0 110-9h.75c.704 0 1.402-.03 2.09-.09m0 9.18c.253.962.584 1.892.985 2.783.247.55.06 1.21-.463 1.511l-.657.38c-.551.318-1.26.117-1.527-.461a20.845 20.845 0 01-1.44-4.282m3.102.069a18.03 18.03 0 01-.59-4.59c0-1.586.205-3.124.59-4.59m0 9.18a23.848 23.848 0 018.835 2.535M10.34 6.66a23.847 23.847 0 008.835-2.535m0 0A23.74 23.74 0 0018.795 3m.38 1.125a23.91 23.91 0 011.014 5.395m-1.014 8.855c-.118.38-.245.754-.38 1.125m.38-1.125a23.91 23.91 0 001.014-5.395m0-3.46c.495.413.811 1.035.811 1.73 0 .695-.316 1.317-.811 1.73m0-3.46a24.347 24.347 0 010 3.46" />
          </svg>
        </div>
        <div class="flex-1 min-w-0">
          <p class="text-sm font-medium text-primary-800 dark:text-primary-200">{{ announcement.title }}</p>
          <p
            class="mt-1 text-sm text-primary-700 dark:text-primary-300 whitespace-pre-wrap announcement-content"
            v-html="linkifyContent(announcement.content)"
          ></p>
        </div>
        <button
          @click="dismissAnnouncement(announcement.id)"
          class="flex-shrink-0 rounded-md p-1 text-primary-500 hover:bg-primary-100 hover:text-primary-700 dark:hover:bg-primary-800/50 dark:hover:text-primary-200 transition-colors"
          :title="t('common.close')"
        >
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { announcementsAPI, type PublicAnnouncement } from '@/api/announcements'

const { t } = useI18n()

// 转义 HTML 特殊字符
const escapeHtml = (text: string): string => {
  const div = document.createElement('div')
  div.textContent = text
  return div.innerHTML
}

// 将文本中的 URL 转换为可点击链接
const linkifyContent = (content: string): string => {
  // 先转义 HTML
  const escaped = escapeHtml(content)
  // URL 正则匹配
  const urlRegex = /(https?:\/\/[^\s<]+)/g
  return escaped.replace(urlRegex, '<a href="$1" target="_blank" rel="noopener noreferrer" class="text-primary-600 dark:text-primary-400 underline hover:text-primary-800 dark:hover:text-primary-200">$1</a>')
}

const announcements = ref<PublicAnnouncement[]>([])
const dismissedIds = ref<Set<number>>(new Set())

const DISMISSED_KEY = 'dismissed_announcements'

const visibleAnnouncements = computed(() => {
  return announcements.value.filter(a => !dismissedIds.value.has(a.id))
})

const loadDismissedIds = () => {
  try {
    const stored = localStorage.getItem(DISMISSED_KEY)
    if (stored) {
      const parsed = JSON.parse(stored)
      dismissedIds.value = new Set(parsed)
    }
  } catch {
    // Ignore parse errors
  }
}

const saveDismissedIds = () => {
  try {
    localStorage.setItem(DISMISSED_KEY, JSON.stringify([...dismissedIds.value]))
  } catch {
    // Ignore storage errors
  }
}

const dismissAnnouncement = (id: number) => {
  dismissedIds.value.add(id)
  saveDismissedIds()
}

const loadAnnouncements = async () => {
  try {
    announcements.value = await announcementsAPI.getAnnouncements()
    // Clean up dismissed IDs that no longer exist
    const currentIds = new Set(announcements.value.map(a => a.id))
    const newDismissedIds = new Set([...dismissedIds.value].filter(id => currentIds.has(id)))
    if (newDismissedIds.size !== dismissedIds.value.size) {
      dismissedIds.value = newDismissedIds
      saveDismissedIds()
    }
  } catch (error) {
    console.error('Failed to load announcements:', error)
  }
}

onMounted(() => {
  loadDismissedIds()
  loadAnnouncements()
})
</script>

<style scoped>
.announcement-enter-active,
.announcement-leave-active {
  transition: all 0.3s ease;
}

.announcement-enter-from,
.announcement-leave-to {
  opacity: 0;
  transform: translateY(-10px);
}
</style>
