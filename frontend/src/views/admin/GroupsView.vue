<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col justify-between gap-4 lg:flex-row lg:items-start">
          <!-- Left: fuzzy search + filters (can wrap to multiple lines) -->
          <div class="flex flex-1 flex-wrap items-center gap-3">
            <div class="relative w-full sm:w-64">
              <Icon
                name="search"
                size="md"
                class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
              />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('admin.groups.searchGroups')"
                class="input pl-10"
                @input="handleSearch"
              />
            </div>
          <Select
            v-model="filters.platform"
            :options="platformFilterOptions"
            :placeholder="t('admin.groups.allPlatforms')"
            class="w-44"
            @change="loadGroups"
          />
          <Select
            v-model="filters.status"
            :options="statusOptions"
            :placeholder="t('admin.groups.allStatus')"
            class="w-40"
            @change="loadGroups"
          />
          <Select
            v-model="filters.is_exclusive"
            :options="exclusiveOptions"
            :placeholder="t('admin.groups.allGroups')"
            class="w-44"
            @change="loadGroups"
          />
          </div>

          <!-- Right: actions -->
          <div class="flex w-full flex-shrink-0 flex-wrap items-center justify-end gap-3 lg:w-auto">
            <button
              @click="loadGroups"
              :disabled="loading"
              class="btn btn-secondary"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button
              @click="showCreateModal = true"
              class="btn btn-primary"
              data-tour="groups-create-btn"
            >
              <Icon name="plus" size="md" class="mr-2" />
              {{ t('admin.groups.createGroup') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="groups" :loading="loading">
          <template #cell-name="{ value }">
            <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
          </template>

          <template #cell-platform="{ value }">
            <span
              :class="[
                'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium',
                value === 'anthropic'
                  ? 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
                  : value === 'openai'
                    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
                    : value === 'antigravity'
                      ? 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
                      : 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
              ]"
            >
              <PlatformIcon :platform="value" size="xs" />
              {{ t('admin.groups.platforms.' + value) }}
            </span>
          </template>

          <template #cell-billing_type="{ row }">
            <div class="space-y-1">
              <!-- Type Badge -->
              <span
                :class="[
                  'inline-block rounded-full px-2 py-0.5 text-xs font-medium',
                  row.subscription_type === 'subscription'
                    ? 'bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400'
                    : 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
                ]"
              >
                {{
                  row.subscription_type === 'subscription'
                    ? t('admin.groups.subscription.subscription')
                    : t('admin.groups.subscription.standard')
                }}
              </span>
              <!-- Subscription Limits - compact single line -->
              <div
                v-if="row.subscription_type === 'subscription'"
                class="text-xs text-gray-500 dark:text-gray-400"
              >
                <template
                  v-if="row.daily_limit_usd || row.weekly_limit_usd || row.monthly_limit_usd"
                >
                  <span v-if="row.daily_limit_usd"
                    >${{ row.daily_limit_usd }}/{{ t('admin.groups.limitDay') }}</span
                  >
                  <span
                    v-if="row.daily_limit_usd && (row.weekly_limit_usd || row.monthly_limit_usd)"
                    class="mx-1 text-gray-300 dark:text-gray-600"
                    >·</span
                  >
                  <span v-if="row.weekly_limit_usd"
                    >${{ row.weekly_limit_usd }}/{{ t('admin.groups.limitWeek') }}</span
                  >
                  <span
                    v-if="row.weekly_limit_usd && row.monthly_limit_usd"
                    class="mx-1 text-gray-300 dark:text-gray-600"
                    >·</span
                  >
                  <span v-if="row.monthly_limit_usd"
                    >${{ row.monthly_limit_usd }}/{{ t('admin.groups.limitMonth') }}</span
                  >
                </template>
                <span v-else class="text-gray-400 dark:text-gray-500">{{
                  t('admin.groups.subscription.noLimit')
                }}</span>
              </div>
            </div>
          </template>

          <template #cell-rate_multiplier="{ value }">
            <span class="text-sm text-gray-700 dark:text-gray-300">{{ value }}x</span>
          </template>

          <template #cell-is_exclusive="{ value }">
            <span :class="['badge', value ? 'badge-primary' : 'badge-gray']">
              {{ value ? t('admin.groups.exclusive') : t('admin.groups.public') }}
            </span>
          </template>

          <template #cell-account_count="{ value }">
            <span
              class="inline-flex items-center rounded bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-800 dark:bg-dark-600 dark:text-gray-300"
            >
              {{ t('admin.groups.accountsCount', { count: value || 0 }) }}
            </span>
          </template>

          <template #cell-status="{ value }">
            <span :class="['badge', value === 'active' ? 'badge-success' : 'badge-danger']">
              {{ t('admin.accounts.status.' + value) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                @click="handleEdit(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
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

          <template #empty>
            <EmptyState
              :title="t('admin.groups.noGroupsYet')"
              :description="t('admin.groups.createFirstGroup')"
              :action-text="t('admin.groups.createGroup')"
              @action="showCreateModal = true"
            />
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

    <!-- Create Group Modal -->
    <BaseDialog
      :show="showCreateModal"
      :title="t('admin.groups.createGroup')"
      width="normal"
      @close="closeCreateModal"
    >
      <form id="create-group-form" @submit.prevent="handleCreateGroup" class="space-y-5">
        <div>
          <label class="input-label">{{ t('admin.groups.form.name') }}</label>
          <input
            v-model="createForm.name"
            type="text"
            required
            class="input"
            :placeholder="t('admin.groups.enterGroupName')"
            data-tour="group-form-name"
          />
        </div>
        <div>
          <label class="input-label">{{ t('admin.groups.form.description') }}</label>
          <textarea
            v-model="createForm.description"
            rows="3"
            class="input"
            :placeholder="t('admin.groups.optionalDescription')"
          ></textarea>
        </div>
        <div>
          <label class="input-label">{{ t('admin.groups.form.platform') }}</label>
          <Select
            v-model="createForm.platform"
            :options="platformOptions"
            data-tour="group-form-platform"
          />
          <p class="input-hint">{{ t('admin.groups.platformHint') }}</p>
        </div>
        <div v-if="createForm.subscription_type !== 'subscription'">
          <label class="input-label">{{ t('admin.groups.form.rateMultiplier') }}</label>
          <input
            v-model.number="createForm.rate_multiplier"
            type="number"
            step="0.001"
            min="0.001"
            required
            class="input"
            data-tour="group-form-multiplier"
          />
          <p class="input-hint">{{ t('admin.groups.rateMultiplierHint') }}</p>
        </div>
        <div v-if="createForm.subscription_type !== 'subscription'" data-tour="group-form-exclusive">
          <div class="mb-1.5 flex items-center gap-1">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.groups.form.exclusive') }}
            </label>
            <!-- Help Tooltip -->
            <div class="group relative inline-flex">
              <Icon
                name="questionCircle"
                size="sm"
                :stroke-width="2"
                class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
              />
              <!-- Tooltip Popover -->
              <div class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100">
                <div class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800">
                  <p class="mb-2 text-xs font-medium">{{ t('admin.groups.exclusiveTooltip.title') }}</p>
                  <p class="mb-2 text-xs leading-relaxed text-gray-300">
                    {{ t('admin.groups.exclusiveTooltip.description') }}
                  </p>
                  <div class="rounded bg-gray-800 p-2 dark:bg-gray-700">
                    <p class="text-xs leading-relaxed text-gray-300">
                      <span class="inline-flex items-center gap-1 text-primary-400"><Icon name="lightbulb" size="xs" /> {{ t('admin.groups.exclusiveTooltip.example') }}</span>
                      {{ t('admin.groups.exclusiveTooltip.exampleContent') }}
                    </p>
                  </div>
                  <!-- Arrow -->
                  <div class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"></div>
                </div>
              </div>
            </div>
          </div>
          <div class="flex items-center gap-3">
            <button
              type="button"
              @click="createForm.is_exclusive = !createForm.is_exclusive"
              :class="[
                'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                createForm.is_exclusive ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform',
                  createForm.is_exclusive ? 'translate-x-6' : 'translate-x-1'
                ]"
              />
            </button>
            <span class="text-sm text-gray-500 dark:text-gray-400">
              {{ createForm.is_exclusive ? t('admin.groups.exclusive') : t('admin.groups.public') }}
            </span>
          </div>
        </div>

        <!-- Subscription Configuration -->
        <div class="mt-4 border-t pt-4">
          <div>
            <label class="input-label">{{ t('admin.groups.subscription.type') }}</label>
            <Select v-model="createForm.subscription_type" :options="subscriptionTypeOptions" />
            <p class="input-hint">{{ t('admin.groups.subscription.typeHint') }}</p>
          </div>

          <!-- Subscription limits (only show when subscription type is selected) -->
          <div
            v-if="createForm.subscription_type === 'subscription'"
            class="space-y-4 border-l-2 border-primary-200 pl-4 dark:border-primary-800"
          >
            <div>
              <label class="input-label">{{ t('admin.groups.subscription.dailyLimit') }}</label>
              <input
                v-model.number="createForm.daily_limit_usd"
                type="number"
                step="0.01"
                min="0"
                class="input"
                :placeholder="t('admin.groups.subscription.noLimit')"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.groups.subscription.weeklyLimit') }}</label>
              <input
                v-model.number="createForm.weekly_limit_usd"
                type="number"
                step="0.01"
                min="0"
                class="input"
                :placeholder="t('admin.groups.subscription.noLimit')"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.groups.subscription.monthlyLimit') }}</label>
              <input
                v-model.number="createForm.monthly_limit_usd"
                type="number"
                step="0.01"
                min="0"
                class="input"
                :placeholder="t('admin.groups.subscription.noLimit')"
              />
            </div>
          </div>
        </div>

        <!-- 图片生成计费配置（antigravity 和 gemini 平台） -->
        <div v-if="createForm.platform === 'antigravity' || createForm.platform === 'gemini'" class="border-t pt-4">
          <label class="block mb-2 font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.groups.imagePricing.title') }}
          </label>
          <p class="text-xs text-gray-500 dark:text-gray-400 mb-3">
            {{ t('admin.groups.imagePricing.description') }}
          </p>
          <div class="grid grid-cols-3 gap-3">
            <div>
              <label class="input-label">1K ($)</label>
              <input
                v-model.number="createForm.image_price_1k"
                type="number"
                step="0.001"
                min="0"
                class="input"
                placeholder="0.134"
              />
            </div>
            <div>
              <label class="input-label">2K ($)</label>
              <input
                v-model.number="createForm.image_price_2k"
                type="number"
                step="0.001"
                min="0"
                class="input"
                placeholder="0.134"
              />
            </div>
            <div>
              <label class="input-label">4K ($)</label>
              <input
                v-model.number="createForm.image_price_4k"
                type="number"
                step="0.001"
                min="0"
                class="input"
                placeholder="0.268"
              />
            </div>
          </div>
        </div>

        <!-- Claude Code 客户端限制（仅 anthropic 平台） -->
        <div v-if="createForm.platform === 'anthropic'" class="border-t pt-4">
          <div class="mb-1.5 flex items-center gap-1">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.groups.claudeCode.title') }}
            </label>
            <!-- Help Tooltip -->
            <div class="group relative inline-flex">
              <Icon
                name="questionCircle"
                size="sm"
                :stroke-width="2"
                class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
              />
              <div class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100">
                <div class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800">
                  <p class="text-xs leading-relaxed text-gray-300">
                    {{ t('admin.groups.claudeCode.tooltip') }}
                  </p>
                  <div class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"></div>
                </div>
              </div>
            </div>
          </div>
          <div class="flex items-center gap-3">
            <button
              type="button"
              @click="createForm.claude_code_only = !createForm.claude_code_only"
              :class="[
                'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                createForm.claude_code_only ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform',
                  createForm.claude_code_only ? 'translate-x-6' : 'translate-x-1'
                ]"
              />
            </button>
            <span class="text-sm text-gray-500 dark:text-gray-400">
              {{ createForm.claude_code_only ? t('admin.groups.claudeCode.enabled') : t('admin.groups.claudeCode.disabled') }}
            </span>
          </div>
          <!-- 降级分组选择（仅当启用 claude_code_only 时显示） -->
          <div v-if="createForm.claude_code_only" class="mt-3">
            <label class="input-label">{{ t('admin.groups.claudeCode.fallbackGroup') }}</label>
            <Select
              v-model="createForm.fallback_group_id"
              :options="fallbackGroupOptions"
              :placeholder="t('admin.groups.claudeCode.noFallback')"
            />
            <p class="input-hint">{{ t('admin.groups.claudeCode.fallbackHint') }}</p>
          </div>
        </div>

        <!-- 模型白名单 -->
        <div class="border-t pt-4">
          <div class="mb-1.5 flex items-center gap-1">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.groups.modelWhitelist.title') }}
            </label>
            <div class="group relative inline-flex">
              <Icon
                name="questionCircle"
                size="sm"
                :stroke-width="2"
                class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
              />
              <div class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100">
                <div class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800">
                  <p class="text-xs leading-relaxed text-gray-300">
                    {{ t('admin.groups.modelWhitelist.tooltip') }}
                  </p>
                  <div class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"></div>
                </div>
              </div>
            </div>
          </div>
          <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.groups.modelWhitelist.description') }}
          </p>
          <!-- 输入框添加模型 -->
          <div class="flex gap-2 mb-2">
            <input
              v-model="createModelInput"
              type="text"
              class="input flex-1"
              :placeholder="t('admin.groups.modelWhitelist.inputPlaceholder')"
              @keyup.enter="addModelToCreate"
            />
            <button
              type="button"
              class="btn btn-secondary"
              @click="addModelToCreate"
            >
              {{ t('common.add') }}
            </button>
          </div>
          <!-- 已添加的模型标签 -->
          <div v-if="createForm.allowed_models.length > 0" class="flex flex-wrap gap-2 p-2 rounded border border-gray-200 dark:border-gray-700">
            <span
              v-for="model in createForm.allowed_models"
              :key="model"
              class="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400"
            >
              {{ model }}
              <button
                type="button"
                class="hover:text-primary-900 dark:hover:text-primary-200"
                @click="removeModelFromCreate(model)"
              >
                <Icon name="x" size="xs" />
              </button>
            </span>
          </div>
          <p v-else class="text-xs text-gray-400 dark:text-gray-500 italic">
            {{ t('admin.groups.modelWhitelist.empty') }}
          </p>
        </div>

        <!-- 模型映射 -->
        <div class="border-t pt-4">
          <div class="mb-1.5 flex items-center gap-1">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.groups.modelMapping.title') }}
            </label>
            <div class="group relative inline-flex">
              <Icon
                name="questionCircle"
                size="sm"
                :stroke-width="2"
                class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
              />
              <div class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100">
                <div class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800">
                  <p class="text-xs leading-relaxed text-gray-300">
                    {{ t('admin.groups.modelMapping.tooltip') }}
                  </p>
                  <div class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"></div>
                </div>
              </div>
            </div>
          </div>
          <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.groups.modelMapping.description') }}
          </p>
          <!-- 输入框添加映射 -->
          <div class="flex gap-2 mb-2">
            <input
              v-model="createMappingKey"
              type="text"
              class="input flex-1"
              :placeholder="t('admin.groups.modelMapping.keyPlaceholder')"
            />
            <span class="flex items-center text-gray-400">→</span>
            <input
              v-model="createMappingValue"
              type="text"
              class="input flex-1"
              :placeholder="t('admin.groups.modelMapping.valuePlaceholder')"
              @keyup.enter="addMappingToCreate"
            />
            <button
              type="button"
              class="btn btn-secondary"
              @click="addMappingToCreate"
            >
              {{ t('common.add') }}
            </button>
          </div>
          <!-- 已添加的映射 -->
          <div v-if="Object.keys(createForm.model_mapping).length > 0" class="space-y-1 p-2 rounded border border-gray-200 dark:border-gray-700">
            <div
              v-for="(value, key) in createForm.model_mapping"
              :key="key"
              class="flex items-center gap-2 text-xs"
            >
              <span class="px-2 py-1 rounded bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">
                {{ key }}
              </span>
              <span class="text-gray-400">→</span>
              <span class="px-2 py-1 rounded bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400">
                {{ value }}
              </span>
              <button
                type="button"
                class="ml-auto text-gray-400 hover:text-red-500"
                @click="removeMappingFromCreate(key as string)"
              >
                <Icon name="x" size="xs" />
              </button>
            </div>
          </div>
          <p v-else class="text-xs text-gray-400 dark:text-gray-500 italic">
            {{ t('admin.groups.modelMapping.empty') }}
          </p>
        </div>

      </form>

      <template #footer>
        <div class="flex justify-end gap-3 pt-4">
          <button @click="closeCreateModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="create-group-form"
            :disabled="submitting"
            class="btn btn-primary"
            data-tour="group-form-submit"
          >
            <svg
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            {{ submitting ? t('admin.groups.creating') : t('common.create') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Edit Group Modal -->
    <BaseDialog
      :show="showEditModal"
      :title="t('admin.groups.editGroup')"
      width="normal"
      @close="closeEditModal"
    >
      <form
        v-if="editingGroup"
        id="edit-group-form"
        @submit.prevent="handleUpdateGroup"
        class="space-y-5"
      >
        <div>
          <label class="input-label">{{ t('admin.groups.form.name') }}</label>
          <input
            v-model="editForm.name"
            type="text"
            required
            class="input"
            data-tour="edit-group-form-name"
          />
        </div>
        <div>
          <label class="input-label">{{ t('admin.groups.form.description') }}</label>
          <textarea v-model="editForm.description" rows="3" class="input"></textarea>
        </div>
        <div>
          <label class="input-label">{{ t('admin.groups.form.platform') }}</label>
          <Select
            v-model="editForm.platform"
            :options="platformOptions"
            :disabled="true"
            data-tour="group-form-platform"
          />
          <p class="input-hint">{{ t('admin.groups.platformNotEditable') }}</p>
        </div>
        <div v-if="editForm.subscription_type !== 'subscription'">
          <label class="input-label">{{ t('admin.groups.form.rateMultiplier') }}</label>
          <input
            v-model.number="editForm.rate_multiplier"
            type="number"
            step="0.001"
            min="0.001"
            required
            class="input"
            data-tour="group-form-multiplier"
          />
        </div>
        <div v-if="editForm.subscription_type !== 'subscription'">
          <div class="mb-1.5 flex items-center gap-1">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.groups.form.exclusive') }}
            </label>
            <!-- Help Tooltip -->
            <div class="group relative inline-flex">
              <Icon
                name="questionCircle"
                size="sm"
                :stroke-width="2"
                class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
              />
              <!-- Tooltip Popover -->
              <div class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100">
                <div class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800">
                  <p class="mb-2 text-xs font-medium">{{ t('admin.groups.exclusiveTooltip.title') }}</p>
                  <p class="mb-2 text-xs leading-relaxed text-gray-300">
                    {{ t('admin.groups.exclusiveTooltip.description') }}
                  </p>
                  <div class="rounded bg-gray-800 p-2 dark:bg-gray-700">
                    <p class="text-xs leading-relaxed text-gray-300">
                      <span class="inline-flex items-center gap-1 text-primary-400"><Icon name="lightbulb" size="xs" /> {{ t('admin.groups.exclusiveTooltip.example') }}</span>
                      {{ t('admin.groups.exclusiveTooltip.exampleContent') }}
                    </p>
                  </div>
                  <!-- Arrow -->
                  <div class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"></div>
                </div>
              </div>
            </div>
          </div>
          <div class="flex items-center gap-3">
            <button
              type="button"
              @click="editForm.is_exclusive = !editForm.is_exclusive"
              :class="[
                'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                editForm.is_exclusive ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform',
                  editForm.is_exclusive ? 'translate-x-6' : 'translate-x-1'
                ]"
              />
            </button>
            <span class="text-sm text-gray-500 dark:text-gray-400">
              {{ editForm.is_exclusive ? t('admin.groups.exclusive') : t('admin.groups.public') }}
            </span>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('admin.groups.form.status') }}</label>
          <Select v-model="editForm.status" :options="editStatusOptions" />
        </div>

        <!-- Subscription Configuration -->
        <div class="mt-4 border-t pt-4">
          <div>
            <label class="input-label">{{ t('admin.groups.subscription.type') }}</label>
            <Select
              v-model="editForm.subscription_type"
              :options="subscriptionTypeOptions"
              :disabled="true"
            />
            <p class="input-hint">{{ t('admin.groups.subscription.typeNotEditable') }}</p>
          </div>

          <!-- Subscription limits (only show when subscription type is selected) -->
          <div
            v-if="editForm.subscription_type === 'subscription'"
            class="space-y-4 border-l-2 border-primary-200 pl-4 dark:border-primary-800"
          >
            <div>
              <label class="input-label">{{ t('admin.groups.subscription.dailyLimit') }}</label>
              <input
                v-model.number="editForm.daily_limit_usd"
                type="number"
                step="0.01"
                min="0"
                class="input"
                :placeholder="t('admin.groups.subscription.noLimit')"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.groups.subscription.weeklyLimit') }}</label>
              <input
                v-model.number="editForm.weekly_limit_usd"
                type="number"
                step="0.01"
                min="0"
                class="input"
                :placeholder="t('admin.groups.subscription.noLimit')"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.groups.subscription.monthlyLimit') }}</label>
              <input
                v-model.number="editForm.monthly_limit_usd"
                type="number"
                step="0.01"
                min="0"
                class="input"
                :placeholder="t('admin.groups.subscription.noLimit')"
              />
            </div>
          </div>
        </div>

        <!-- 图片生成计费配置（antigravity 和 gemini 平台） -->
        <div v-if="editForm.platform === 'antigravity' || editForm.platform === 'gemini'" class="border-t pt-4">
          <label class="block mb-2 font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.groups.imagePricing.title') }}
          </label>
          <p class="text-xs text-gray-500 dark:text-gray-400 mb-3">
            {{ t('admin.groups.imagePricing.description') }}
          </p>
          <div class="grid grid-cols-3 gap-3">
            <div>
              <label class="input-label">1K ($)</label>
              <input
                v-model.number="editForm.image_price_1k"
                type="number"
                step="0.001"
                min="0"
                class="input"
                placeholder="0.134"
              />
            </div>
            <div>
              <label class="input-label">2K ($)</label>
              <input
                v-model.number="editForm.image_price_2k"
                type="number"
                step="0.001"
                min="0"
                class="input"
                placeholder="0.134"
              />
            </div>
            <div>
              <label class="input-label">4K ($)</label>
              <input
                v-model.number="editForm.image_price_4k"
                type="number"
                step="0.001"
                min="0"
                class="input"
                placeholder="0.268"
              />
            </div>
          </div>
        </div>

        <!-- Claude Code 客户端限制（仅 anthropic 平台） -->
        <div v-if="editForm.platform === 'anthropic'" class="border-t pt-4">
          <div class="mb-1.5 flex items-center gap-1">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.groups.claudeCode.title') }}
            </label>
            <!-- Help Tooltip -->
            <div class="group relative inline-flex">
              <Icon
                name="questionCircle"
                size="sm"
                :stroke-width="2"
                class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
              />
              <div class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100">
                <div class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800">
                  <p class="text-xs leading-relaxed text-gray-300">
                    {{ t('admin.groups.claudeCode.tooltip') }}
                  </p>
                  <div class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"></div>
                </div>
              </div>
            </div>
          </div>
          <div class="flex items-center gap-3">
            <button
              type="button"
              @click="editForm.claude_code_only = !editForm.claude_code_only"
              :class="[
                'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                editForm.claude_code_only ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform',
                  editForm.claude_code_only ? 'translate-x-6' : 'translate-x-1'
                ]"
              />
            </button>
            <span class="text-sm text-gray-500 dark:text-gray-400">
              {{ editForm.claude_code_only ? t('admin.groups.claudeCode.enabled') : t('admin.groups.claudeCode.disabled') }}
            </span>
          </div>
          <!-- 降级分组选择（仅当启用 claude_code_only 时显示） -->
          <div v-if="editForm.claude_code_only" class="mt-3">
            <label class="input-label">{{ t('admin.groups.claudeCode.fallbackGroup') }}</label>
            <Select
              v-model="editForm.fallback_group_id"
              :options="fallbackGroupOptionsForEdit"
              :placeholder="t('admin.groups.claudeCode.noFallback')"
            />
            <p class="input-hint">{{ t('admin.groups.claudeCode.fallbackHint') }}</p>
          </div>
        </div>

        <!-- 模型白名单 -->
        <div class="border-t pt-4">
          <div class="mb-1.5 flex items-center gap-1">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.groups.modelWhitelist.title') }}
            </label>
            <div class="group relative inline-flex">
              <Icon
                name="questionCircle"
                size="sm"
                :stroke-width="2"
                class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
              />
              <div class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100">
                <div class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800">
                  <p class="text-xs leading-relaxed text-gray-300">
                    {{ t('admin.groups.modelWhitelist.tooltip') }}
                  </p>
                  <div class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"></div>
                </div>
              </div>
            </div>
          </div>
          <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.groups.modelWhitelist.description') }}
          </p>
          <!-- 输入框添加模型 -->
          <div class="flex gap-2 mb-2">
            <input
              v-model="editModelInput"
              type="text"
              class="input flex-1"
              :placeholder="t('admin.groups.modelWhitelist.inputPlaceholder')"
              @keyup.enter="addModelToEdit"
            />
            <button
              type="button"
              class="btn btn-secondary"
              @click="addModelToEdit"
            >
              {{ t('common.add') }}
            </button>
          </div>
          <!-- 已添加的模型标签 -->
          <div v-if="editForm.allowed_models.length > 0" class="flex flex-wrap gap-2 p-2 rounded border border-gray-200 dark:border-gray-700">
            <span
              v-for="model in editForm.allowed_models"
              :key="model"
              class="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400"
            >
              {{ model }}
              <button
                type="button"
                class="hover:text-primary-900 dark:hover:text-primary-200"
                @click="removeModelFromEdit(model)"
              >
                <Icon name="x" size="xs" />
              </button>
            </span>
          </div>
          <p v-else class="text-xs text-gray-400 dark:text-gray-500 italic">
            {{ t('admin.groups.modelWhitelist.empty') }}
          </p>
        </div>

        <!-- 模型映射 -->
        <div class="border-t pt-4">
          <div class="mb-1.5 flex items-center gap-1">
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.groups.modelMapping.title') }}
            </label>
            <div class="group relative inline-flex">
              <Icon
                name="questionCircle"
                size="sm"
                :stroke-width="2"
                class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
              />
              <div class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100">
                <div class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800">
                  <p class="text-xs leading-relaxed text-gray-300">
                    {{ t('admin.groups.modelMapping.tooltip') }}
                  </p>
                  <div class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"></div>
                </div>
              </div>
            </div>
          </div>
          <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.groups.modelMapping.description') }}
          </p>
          <!-- 输入框添加映射 -->
          <div class="flex gap-2 mb-2">
            <input
              v-model="editMappingKey"
              type="text"
              class="input flex-1"
              :placeholder="t('admin.groups.modelMapping.keyPlaceholder')"
            />
            <span class="flex items-center text-gray-400">→</span>
            <input
              v-model="editMappingValue"
              type="text"
              class="input flex-1"
              :placeholder="t('admin.groups.modelMapping.valuePlaceholder')"
              @keyup.enter="addMappingToEdit"
            />
            <button
              type="button"
              class="btn btn-secondary"
              @click="addMappingToEdit"
            >
              {{ t('common.add') }}
            </button>
          </div>
          <!-- 已添加的映射 -->
          <div v-if="Object.keys(editForm.model_mapping).length > 0" class="space-y-1 p-2 rounded border border-gray-200 dark:border-gray-700">
            <div
              v-for="(value, key) in editForm.model_mapping"
              :key="key"
              class="flex items-center gap-2 text-xs"
            >
              <span class="px-2 py-1 rounded bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">
                {{ key }}
              </span>
              <span class="text-gray-400">→</span>
              <span class="px-2 py-1 rounded bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400">
                {{ value }}
              </span>
              <button
                type="button"
                class="ml-auto text-gray-400 hover:text-red-500"
                @click="removeMappingFromEdit(key as string)"
              >
                <Icon name="x" size="xs" />
              </button>
            </div>
          </div>
          <p v-else class="text-xs text-gray-400 dark:text-gray-500 italic">
            {{ t('admin.groups.modelMapping.empty') }}
          </p>
        </div>

      </form>

      <template #footer>
        <div class="flex justify-end gap-3 pt-4">
          <button @click="closeEditModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="edit-group-form"
            :disabled="submitting"
            class="btn btn-primary"
            data-tour="group-form-submit"
          >
            <svg
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            {{ submitting ? t('admin.groups.updating') : t('common.update') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.groups.deleteGroup')"
      :message="deleteConfirmMessage"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useOnboardingStore } from '@/stores/onboarding'
import { adminAPI } from '@/api/admin'
import type { Group, GroupPlatform, SubscriptionType } from '@/types'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const onboardingStore = useOnboardingStore()

const columns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.groups.columns.name'), sortable: true },
  { key: 'platform', label: t('admin.groups.columns.platform'), sortable: true },
  { key: 'billing_type', label: t('admin.groups.columns.billingType'), sortable: true },
  { key: 'rate_multiplier', label: t('admin.groups.columns.rateMultiplier'), sortable: true },
  { key: 'is_exclusive', label: t('admin.groups.columns.type'), sortable: true },
  { key: 'account_count', label: t('admin.groups.columns.accounts'), sortable: true },
  { key: 'status', label: t('admin.groups.columns.status'), sortable: true },
  { key: 'actions', label: t('admin.groups.columns.actions'), sortable: false }
])

// Filter options
const statusOptions = computed(() => [
  { value: '', label: t('admin.groups.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') }
])

const exclusiveOptions = computed(() => [
  { value: '', label: t('admin.groups.allGroups') },
  { value: 'true', label: t('admin.groups.exclusive') },
  { value: 'false', label: t('admin.groups.nonExclusive') }
])

const platformOptions = computed(() => [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' }
])

const platformFilterOptions = computed(() => [
  { value: '', label: t('admin.groups.allPlatforms') },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' }
])

const editStatusOptions = computed(() => [
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') }
])

const subscriptionTypeOptions = computed(() => [
  { value: 'standard', label: t('admin.groups.subscription.standard') },
  { value: 'subscription', label: t('admin.groups.subscription.subscription') }
])

// 降级分组选项（创建时）- 仅包含 anthropic 平台且未启用 claude_code_only 的分组
const fallbackGroupOptions = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t('admin.groups.claudeCode.noFallback') }
  ]
  const eligibleGroups = groups.value.filter(
    (g) => g.platform === 'anthropic' && !g.claude_code_only && g.status === 'active'
  )
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name })
  })
  return options
})

// 降级分组选项（编辑时）- 排除自身
const fallbackGroupOptionsForEdit = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t('admin.groups.claudeCode.noFallback') }
  ]
  const currentId = editingGroup.value?.id
  const eligibleGroups = groups.value.filter(
    (g) => g.platform === 'anthropic' && !g.claude_code_only && g.status === 'active' && g.id !== currentId
  )
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name })
  })
  return options
})

const groups = ref<Group[]>([])
const loading = ref(false)
const searchQuery = ref('')

// 模型白名单输入
const createModelInput = ref('')
const editModelInput = ref('')

// 模型映射输入
const createMappingKey = ref('')
const createMappingValue = ref('')
const editMappingKey = ref('')
const editMappingValue = ref('')

const addModelToCreate = () => {
  const model = createModelInput.value.trim()
  if (model && !createForm.allowed_models.includes(model)) {
    createForm.allowed_models.push(model)
  }
  createModelInput.value = ''
}

const removeModelFromCreate = (model: string) => {
  const index = createForm.allowed_models.indexOf(model)
  if (index > -1) {
    createForm.allowed_models.splice(index, 1)
  }
}

const addModelToEdit = () => {
  const model = editModelInput.value.trim()
  if (model && !editForm.allowed_models.includes(model)) {
    editForm.allowed_models.push(model)
  }
  editModelInput.value = ''
}

const removeModelFromEdit = (model: string) => {
  const index = editForm.allowed_models.indexOf(model)
  if (index > -1) {
    editForm.allowed_models.splice(index, 1)
  }
}

// 模型映射管理函数
const addMappingToCreate = () => {
  const key = createMappingKey.value.trim()
  const value = createMappingValue.value.trim()
  if (key && value && !createForm.model_mapping[key]) {
    createForm.model_mapping[key] = value
  }
  createMappingKey.value = ''
  createMappingValue.value = ''
}

const removeMappingFromCreate = (key: string) => {
  delete createForm.model_mapping[key]
}

const addMappingToEdit = () => {
  const key = editMappingKey.value.trim()
  const value = editMappingValue.value.trim()
  if (key && value && !editForm.model_mapping[key]) {
    editForm.model_mapping[key] = value
  }
  editMappingKey.value = ''
  editMappingValue.value = ''
}

const removeMappingFromEdit = (key: string) => {
  delete editForm.model_mapping[key]
}

const filters = reactive({
  platform: '',
  status: '',
  is_exclusive: ''
})
const pagination = reactive({
  page: 1,
  page_size: 20,
  total: 0,
  pages: 0
})

let abortController: AbortController | null = null

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const submitting = ref(false)
const editingGroup = ref<Group | null>(null)
const deletingGroup = ref<Group | null>(null)

const createForm = reactive({
  name: '',
  description: '',
  platform: 'anthropic' as GroupPlatform,
  rate_multiplier: 1.0,
  is_exclusive: false,
  subscription_type: 'standard' as SubscriptionType,
  daily_limit_usd: null as number | null,
  weekly_limit_usd: null as number | null,
  monthly_limit_usd: null as number | null,
  // 图片生成计费配置（仅 antigravity 平台使用）
  image_price_1k: null as number | null,
  image_price_2k: null as number | null,
  image_price_4k: null as number | null,
  // Claude Code 客户端限制（仅 anthropic 平台使用）
  claude_code_only: false,
  fallback_group_id: null as number | null,
  // 模型白名单
  allowed_models: [] as string[],
  // 模型映射
  model_mapping: {} as Record<string, string>
})

const editForm = reactive({
  name: '',
  description: '',
  platform: 'anthropic' as GroupPlatform,
  rate_multiplier: 1.0,
  is_exclusive: false,
  status: 'active' as 'active' | 'inactive',
  subscription_type: 'standard' as SubscriptionType,
  daily_limit_usd: null as number | null,
  weekly_limit_usd: null as number | null,
  monthly_limit_usd: null as number | null,
  // 图片生成计费配置（仅 antigravity 平台使用）
  image_price_1k: null as number | null,
  image_price_2k: null as number | null,
  image_price_4k: null as number | null,
  // Claude Code 客户端限制（仅 anthropic 平台使用）
  claude_code_only: false,
  fallback_group_id: null as number | null,
  // 模型白名单
  allowed_models: [] as string[],
  // 模型映射
  model_mapping: {} as Record<string, string>
})

// 根据分组类型返回不同的删除确认消息
const deleteConfirmMessage = computed(() => {
  if (!deletingGroup.value) {
    return ''
  }
  if (deletingGroup.value.subscription_type === 'subscription') {
    return t('admin.groups.deleteConfirmSubscription', { name: deletingGroup.value.name })
  }
  return t('admin.groups.deleteConfirm', { name: deletingGroup.value.name })
})

const loadGroups = async () => {
  if (abortController) {
    abortController.abort()
  }
  const currentController = new AbortController()
  abortController = currentController
  const { signal } = currentController
  loading.value = true
  try {
    const response = await adminAPI.groups.list(pagination.page, pagination.page_size, {
      platform: (filters.platform as GroupPlatform) || undefined,
      status: filters.status as any,
      is_exclusive: filters.is_exclusive ? filters.is_exclusive === 'true' : undefined,
      search: searchQuery.value.trim() || undefined
    }, { signal })
    if (signal.aborted) return
    groups.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error: any) {
    if (signal.aborted || error?.name === 'AbortError' || error?.code === 'ERR_CANCELED') {
      return
    }
    appStore.showError(t('admin.groups.failedToLoad'))
    console.error('Error loading groups:', error)
  } finally {
    if (abortController === currentController && !signal.aborted) {
      loading.value = false
    }
  }
}

let searchTimeout: ReturnType<typeof setTimeout>
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadGroups()
  }, 300)
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadGroups()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadGroups()
}

const closeCreateModal = () => {
  showCreateModal.value = false
  createForm.name = ''
  createForm.description = ''
  createForm.platform = 'anthropic'
  createForm.rate_multiplier = 1.0
  createForm.is_exclusive = false
  createForm.subscription_type = 'standard'
  createForm.daily_limit_usd = null
  createForm.weekly_limit_usd = null
  createForm.monthly_limit_usd = null
  createForm.image_price_1k = null
  createForm.image_price_2k = null
  createForm.image_price_4k = null
  createForm.claude_code_only = false
  createForm.fallback_group_id = null
  createForm.allowed_models = []
  createForm.model_mapping = {}
}

const handleCreateGroup = async () => {
  if (!createForm.name.trim()) {
    appStore.showError(t('admin.groups.nameRequired'))
    return
  }
  submitting.value = true
  try {
    await adminAPI.groups.create(createForm)
    appStore.showSuccess(t('admin.groups.groupCreated'))
    closeCreateModal()
    loadGroups()
    // Only advance tour if active, on submit step, and creation succeeded
    if (onboardingStore.isCurrentStep('[data-tour="group-form-submit"]')) {
      onboardingStore.nextStep(500)
    }
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.groups.failedToCreate'))
    console.error('Error creating group:', error)
    // Don't advance tour on error
  } finally {
    submitting.value = false
  }
}

const handleEdit = (group: Group) => {
  editingGroup.value = group
  editForm.name = group.name
  editForm.description = group.description || ''
  editForm.platform = group.platform
  editForm.rate_multiplier = group.rate_multiplier
  editForm.is_exclusive = group.is_exclusive
  editForm.status = group.status
  editForm.subscription_type = group.subscription_type || 'standard'
  editForm.daily_limit_usd = group.daily_limit_usd
  editForm.weekly_limit_usd = group.weekly_limit_usd
  editForm.monthly_limit_usd = group.monthly_limit_usd
  editForm.image_price_1k = group.image_price_1k
  editForm.image_price_2k = group.image_price_2k
  editForm.image_price_4k = group.image_price_4k
  editForm.claude_code_only = group.claude_code_only || false
  editForm.fallback_group_id = group.fallback_group_id
  editForm.allowed_models = group.allowed_models || []
  editForm.model_mapping = group.model_mapping || {}
  showEditModal.value = true
}

const closeEditModal = () => {
  showEditModal.value = false
  editingGroup.value = null
}

const handleUpdateGroup = async () => {
  if (!editingGroup.value) return
  if (!editForm.name.trim()) {
    appStore.showError(t('admin.groups.nameRequired'))
    return
  }

  submitting.value = true
  try {
    // 转换 fallback_group_id: null -> 0 (后端使用 0 表示清除)
    const payload = {
      ...editForm,
      fallback_group_id: editForm.fallback_group_id === null ? 0 : editForm.fallback_group_id
    }
    await adminAPI.groups.update(editingGroup.value.id, payload)
    appStore.showSuccess(t('admin.groups.groupUpdated'))
    closeEditModal()
    loadGroups()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.groups.failedToUpdate'))
    console.error('Error updating group:', error)
  } finally {
    submitting.value = false
  }
}

const handleDelete = (group: Group) => {
  deletingGroup.value = group
  showDeleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingGroup.value) return

  try {
    await adminAPI.groups.delete(deletingGroup.value.id)
    appStore.showSuccess(t('admin.groups.groupDeleted'))
    showDeleteDialog.value = false
    deletingGroup.value = null
    loadGroups()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.groups.failedToDelete'))
    console.error('Error deleting group:', error)
  }
}

// 监听 subscription_type 变化，订阅模式时重置 rate_multiplier 为 1，is_exclusive 为 true
watch(
  () => createForm.subscription_type,
  (newVal) => {
    if (newVal === 'subscription') {
      createForm.rate_multiplier = 1.0
      createForm.is_exclusive = true
    }
  }
)

onMounted(() => {
  loadGroups()
})
</script>
