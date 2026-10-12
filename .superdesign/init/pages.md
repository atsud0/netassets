# Key page dependencies

## DashboardView.vue
- web/src/views/DashboardView.vue
  - web/src/api/client.ts
  - web/src/stores/auth.ts
    - web/src/api/client.ts

## AssetsView.vue
- web/src/views/AssetsView.vue
  - web/src/api/client.ts
  - web/src/api/export-jobs.ts
    - web/src/api/client.ts
  - web/src/stores/auth.ts
    - web/src/api/client.ts
  - web/src/components/SavedFilterViews.vue
    - web/src/api/client.ts
  - web/src/components/AssetExternalObservations.vue
    - web/src/api/client.ts
  - web/src/components/WorkingVersionDiffDrawer.vue
    - web/src/api/client.ts
  - web/src/components/ResourceSubmissionHistoryDrawer.vue
    - web/src/api/client.ts
  - web/src/utils/inventory-drafts.ts
  - web/src/utils/import-column-mapping.ts

## SecurityWorkOrdersView.vue
- web/src/views/SecurityWorkOrdersView.vue
  - web/src/api/client.ts
  - web/src/stores/auth.ts
    - web/src/api/client.ts
  - web/src/components/SavedFilterViews.vue
    - web/src/api/client.ts

## RemediationTasksView.vue
- web/src/views/RemediationTasksView.vue
  - web/src/api/client.ts
  - web/src/stores/auth.ts
    - web/src/api/client.ts
  - web/src/components/RepairTaskDetail.vue
    - web/src/api/client.ts
    - web/src/api/remediation.ts
      - web/src/api/client.ts
    - web/src/api/remediation.ts
      - web/src/api/client.ts
  - web/src/api/remediation.ts
    - web/src/api/client.ts
  - web/src/api/remediation.ts
    - web/src/api/client.ts

## RemediationPeopleView.vue
- web/src/views/RemediationPeopleView.vue
  - web/src/api/client.ts
  - web/src/api/remediation.ts
    - web/src/api/client.ts
  - web/src/components/RemediationAdministration.vue
    - web/src/api/client.ts
    - web/src/api/remediation.ts
      - web/src/api/client.ts
  - web/src/stores/auth.ts
    - web/src/api/client.ts
  - web/src/api/remediation.ts
    - web/src/api/client.ts

## LoginView.vue
- web/src/views/LoginView.vue
  - web/src/api/client.ts
  - web/src/stores/auth.ts
    - web/src/api/client.ts
