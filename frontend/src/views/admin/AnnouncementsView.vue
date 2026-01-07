<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex justify-end gap-3">
          <button
            @click="loadAnnouncements"
            :disabled="loading"
            class="btn btn-secondary"
            :title="t('common.refresh')"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button @click="openCreateDialog" class="btn btn-primary">
            {{ t('admin.announcements.create') }}
          </button>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="announcements" :loading="loading">
          <template #cell-title="{ value, row }">
            <div class="max-w-xs">
              <div class="font-medium text-gray-900 dark:text-white truncate">{{ value }}</div>
              <div class="text-sm text-gray-500 dark:text-gray-400 truncate">{{ row.content.substring(0, 50) }}{{ row.content.length > 50 ? '...' : '' }}</div>
            </div>
          </template>

          <template #cell-enabled="{ value, row }">
            <button
              @click="toggleEnabled(row)"
              :class="[
                'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
                value ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                  value ? 'translate-x-5' : 'translate-x-0'
                ]"
              />
            </button>
          </template>

          <template #cell-priority="{ value }">
            <span class="badge badge-primary">{{ value }}</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-gray-400">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center space-x-2">
              <button
                @click="openEditDialog(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-600 dark:hover:text-gray-300"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <button
                @click="handleDelete(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
              >
                <Icon name="trash" size="sm" />
                <span class="text-xs">{{ t('common.delete') }}</span>
              </button>
            </div>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.announcements.deleteAnnouncement')"
      :message="t('admin.announcements.deleteConfirm')"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Create/Edit Dialog -->
    <Teleport to="body">
      <div v-if="showFormDialog" class="fixed inset-0 z-50 flex items-center justify-center">
        <div class="fixed inset-0 bg-black/50" @click="showFormDialog = false"></div>
        <div class="relative z-10 w-full max-w-lg rounded-xl bg-white p-6 shadow-xl dark:bg-dark-800">
          <h2 class="mb-4 text-lg font-semibold text-gray-900 dark:text-white">
            {{ editingAnnouncement ? t('admin.announcements.edit') : t('admin.announcements.create') }}
          </h2>
          <form @submit.prevent="handleSubmit" class="space-y-4">
            <div>
              <label class="input-label">{{ t('admin.announcements.columns.title') }}</label>
              <input
                v-model="form.title"
                type="text"
                required
                maxlength="200"
                class="input"
                :placeholder="t('admin.announcements.titlePlaceholder')"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.announcements.columns.content') }}</label>
              <textarea
                v-model="form.content"
                required
                rows="4"
                class="input"
                :placeholder="t('admin.announcements.contentPlaceholder')"
              ></textarea>
            </div>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="input-label">{{ t('admin.announcements.columns.priority') }}</label>
                <input
                  v-model.number="form.priority"
                  type="number"
                  min="0"
                  max="100"
                  class="input"
                />
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.announcements.priorityHint') }}</p>
              </div>
              <div>
                <label class="input-label">{{ t('admin.announcements.columns.enabled') }}</label>
                <div class="mt-2">
                  <button
                    type="button"
                    @click="form.enabled = !form.enabled"
                    :class="[
                      'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
                      form.enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
                    ]"
                  >
                    <span
                      :class="[
                        'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                        form.enabled ? 'translate-x-5' : 'translate-x-0'
                      ]"
                    />
                  </button>
                </div>
              </div>
            </div>
            <div class="flex justify-end gap-3 pt-2">
              <button type="button" @click="showFormDialog = false" class="btn btn-secondary">
                {{ t('common.cancel') }}
              </button>
              <button type="submit" :disabled="submitting" class="btn btn-primary">
                {{ submitting ? t('common.saving') : t('common.save') }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { Announcement } from '@/api/admin/announcements'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()

const columns = computed<Column[]>(() => [
  { key: 'title', label: t('admin.announcements.columns.title') },
  { key: 'enabled', label: t('admin.announcements.columns.enabled') },
  { key: 'priority', label: t('admin.announcements.columns.priority'), sortable: true },
  { key: 'created_at', label: t('admin.announcements.columns.createdAt'), sortable: true },
  { key: 'actions', label: t('admin.announcements.columns.actions') }
])

const announcements = ref<Announcement[]>([])
const loading = ref(false)
const submitting = ref(false)

const pagination = reactive({
  page: 1,
  page_size: 20,
  total: 0
})

let abortController: AbortController | null = null

const showDeleteDialog = ref(false)
const showFormDialog = ref(false)
const deletingAnnouncement = ref<Announcement | null>(null)
const editingAnnouncement = ref<Announcement | null>(null)

const form = reactive({
  title: '',
  content: '',
  enabled: false,
  priority: 0
})

const resetForm = () => {
  form.title = ''
  form.content = ''
  form.enabled = false
  form.priority = 0
}

const openCreateDialog = () => {
  editingAnnouncement.value = null
  resetForm()
  showFormDialog.value = true
}

const openEditDialog = (announcement: Announcement) => {
  editingAnnouncement.value = announcement
  form.title = announcement.title
  form.content = announcement.content
  form.enabled = announcement.enabled
  form.priority = announcement.priority
  showFormDialog.value = true
}

const loadAnnouncements = async () => {
  if (abortController) {
    abortController.abort()
  }
  const currentController = new AbortController()
  abortController = currentController
  loading.value = true
  try {
    const response = await adminAPI.announcements.list(
      pagination.page,
      pagination.page_size,
      { signal: currentController.signal }
    )
    if (currentController.signal.aborted) {
      return
    }
    announcements.value = response.items
    pagination.total = response.total
  } catch (error: any) {
    if (
      currentController.signal.aborted ||
      error?.name === 'AbortError' ||
      error?.code === 'ERR_CANCELED'
    ) {
      return
    }
    appStore.showError(t('admin.announcements.failedToLoad'))
    console.error('Error loading announcements:', error)
  } finally {
    if (abortController === currentController && !currentController.signal.aborted) {
      loading.value = false
      abortController = null
    }
  }
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadAnnouncements()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadAnnouncements()
}

const handleSubmit = async () => {
  submitting.value = true
  try {
    if (editingAnnouncement.value) {
      await adminAPI.announcements.update(editingAnnouncement.value.id, {
        title: form.title,
        content: form.content,
        enabled: form.enabled,
        priority: form.priority
      })
      appStore.showSuccess(t('admin.announcements.updated'))
    } else {
      await adminAPI.announcements.create({
        title: form.title,
        content: form.content,
        enabled: form.enabled,
        priority: form.priority
      })
      appStore.showSuccess(t('admin.announcements.created'))
    }
    showFormDialog.value = false
    loadAnnouncements()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.announcements.failedToSave'))
    console.error('Error saving announcement:', error)
  } finally {
    submitting.value = false
  }
}

const toggleEnabled = async (announcement: Announcement) => {
  try {
    await adminAPI.announcements.update(announcement.id, {
      enabled: !announcement.enabled
    })
    announcement.enabled = !announcement.enabled
    appStore.showSuccess(t('admin.announcements.statusUpdated'))
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.announcements.failedToUpdate'))
    console.error('Error toggling announcement:', error)
  }
}

const handleDelete = (announcement: Announcement) => {
  deletingAnnouncement.value = announcement
  showDeleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingAnnouncement.value) return

  try {
    await adminAPI.announcements.delete(deletingAnnouncement.value.id)
    appStore.showSuccess(t('admin.announcements.deleted'))
    showDeleteDialog.value = false
    deletingAnnouncement.value = null
    loadAnnouncements()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.announcements.failedToDelete'))
    console.error('Error deleting announcement:', error)
  }
}

onMounted(() => {
  loadAnnouncements()
})
</script>
