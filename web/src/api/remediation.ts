import { apiClient } from './client'

export interface Page<T> { items: T[]; total: number; page: number; page_size: number }
export interface Material {
  attachment_ids?: string[]; title: string; severity: string; affected_location: string; description: string; evidence: string; remediation_advice: string; requirement: string
}
export interface RepairTask extends Material {
 expected_completion_at?: string; blocked_dependency: string; blocked_until?: string; material_needs_review: boolean; source_revision: number
 source_material?: Material; no_deployment_reason?: string; material_attachments: Attachment[]
  id: string; task_no: string; work_order_id: string; work_order_no: string; work_order_status: string; finding_id: string
  organization_id: string; organization_name: string; business_system_id: string; system_name: string
  assignee_user_id: string; assignee_name: string; vendor_name: string; status: string; completion_reason?: string; risk_acceptance_current?: boolean
  version: number; material_revision: number; current_submission_id?: string; due_at: string; original_due_at: string
  target_environment: string; deployment_required: boolean; progress: string; blocked_reason: string; return_reason: string
  overdue: boolean; needs_reassignment: boolean; can_assign?: boolean; can_review?: boolean; can_verify?: boolean; can_execute?: boolean
}
export interface Grant {
  id: string; user_id: string; username: string; display_name: string; organization_id: string; organization_name: string
  business_system_id: string; system_name: string; vendor_id?: string; vendor_name: string; participant_type: string
  status: string; valid_until: string; available: boolean; active_task_count: number
}
export interface SystemOption { id: string; system_name: string; organization_id: string; organization_name: string }
export interface Vendor { id: string; name: string; organization_id: string; organization_name: string; contact: string; status: string; catalog_id: string; global_status: string }
export interface Attachment { id: string; original_name: string; visibility: string; size_bytes: number; created_at: string }
export interface Submission {
  material?: Material; no_deployment_reason?: string
  id: string; revision: number; material_revision: number; remediation_summary: string; deployment_status: string
  target_environment: string; change_reference: string; deployed_at?: string; self_test_result: string; lifecycle_status: string; submitted_at: string
  attachments: Attachment[]; reviews: { stage: string; decision: string; public_feedback: string; internal_note: string; reviewed_at: string; verified_at?: string; verified_environment: string; reviewer_name: string }[]
}
export interface Comment { id: string; author_name: string; visibility: string; body: string; created_at: string }
export interface RepairRequest { id: string; type: string; status: string; reason: string; mitigation: string; proposed_due_at?: string; decision: string; created_at: string }
export interface TaskEvent { id: number; action: string; actor_name: string; created_at: string; details: Record<string, string> }
export const taskStatuses: Record<string, string> = {
  pending_acceptance: '待接收', in_progress: '修复中', pending_org_review: '待组织确认', pending_security_review: '待集团复测', completed: '已完成', cancelled: '已取消',
}
export const severityLabels: Record<string, string> = { critical: '严重', high: '高危', medium: '中危', low: '低危', info: '提示' }
export const outcomes: Record<string, string> = { verified_fixed: '复测通过', risk_accepted: '已接受风险', false_positive: '确认为误报' }
export const requestLabels: Record<string, string> = { reassign: '申请改派', extension: '申请延期', false_positive: '疑似误报', risk_acceptance: '申请风险接受' }
export const eventLabels: Record<string, string> = {
  'source-update': '原始材料更新', 'reopen-risk': '撤销风险接受', assign: '派发任务', accept: '接收任务', progress: '更新进展', submit: '提交修复', withdraw: '撤回提交',
  'organization-review': '组织确认', 'security-review': '集团复测', reassign: '改派任务', cancel: '取消任务',
  materials: '更新共享材料', comment: '追加沟通', 'attachment.upload': '上传附件', 'request.create': '提出申请', 'request.decide': '处理申请',
}
export function dateText(value?: string) { return value ? new Date(value).toLocaleString('zh-CN') : '—' }
export function newKey() {
  // Private deployments also run over HTTP LAN addresses, where randomUUID is
  // unavailable. getRandomValues remains available without a secure context.
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
}
// Select controls consume every page rather than silently hiding options after 100 rows.
export async function loadOptions<T>(path: string, params: Record<string, string> = {}): Promise<T[]> {
  const items: T[] = []
  for (let page = 1; ; page++) {
    const { data } = await apiClient.get<Page<T>>(path, { params: { ...params, page, page_size: 100 } })
    items.push(...data.items)
    if (items.length >= data.total || !data.items.length) return items
  }
}

export interface RepairStatistics { total: number; verified_fixed: number; risk_accepted: number; risk_expired: number; false_positive: number; overdue: number; extended: number; execution_seconds: number; organization_seconds: number; security_seconds: number; counts: Record<string, number>; workload: { organization_name: string; assignee_name: string; vendor_name: string; total: number; active: number }[] }
export interface MaterialVersion extends Material { revision: number; source_revision: number; target_environment: string; deployment_required: boolean; attachments: Attachment[]; created_at: string }
