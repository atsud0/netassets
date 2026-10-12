# Shared patterns
## AppShell
- Source: web/src/layouts/AppLayout.vue
- Category: layout
- Description: Role-aware brand, sidebar, organization selector and identity topbar.
- Extractable props: activePath, collapsed, executionOnly, unreadNotificationCount
- Hardcoded: existing NA brand mark, labels, styles and navigation groups.
## SavedFilterViews
- Source: web/src/components/SavedFilterViews.vue
- Category: basic
- Description: Personal named filters, suitable for asset and order lists.
- Props: pageKey,currentFilters,disabled
## RepairTaskDetail
- Source: web/src/components/RepairTaskDetail.vue
- Category: layout
- Description: Seven-tab task detail and action forms; proposed as split evidence/review workspace.
- Props: id,executor
