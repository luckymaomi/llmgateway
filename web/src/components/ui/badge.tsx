import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

export type BadgeTone = 'neutral' | 'positive' | 'warning' | 'danger' | 'info'

export function Badge({ children, tone = 'neutral' }: { children: ReactNode; tone?: BadgeTone }) {
  return <span className={cn('badge', `badge--${tone}`)}>{children}</span>
}

const statusLabels: Record<string, string> = {
  active: '可用',
  disabled: '已停用',
  pending: '待处理',
  pending_review: '待审核',
  cooling: '冷却中',
  unknown: '未知',
  healthy: '健康',
  degraded: '降级',
  unavailable: '不可用',
  issued: '已签发',
  claimed: '已领取',
  approved: '已批准',
  expired: '已过期',
  revoked: '已撤销',
  suspended: '已停用',
  draft: '草稿',
  validating: '校验中',
  published: '已发布',
  superseded: '已替换',
  invalid: '校验失败',
  scheduled: '待生效',
  retained: '留存中',
  deletion_scheduled: '待删除',
  deleted: '已删除',
  queued: '排队中',
  admitted: '已准入',
  dispatching: '发送中',
  streaming: '流式返回',
  completed: '已完成',
  failed: '失败',
  canceled: '已取消',
  uncertain: '待确认',
}

function statusTone(status: string): BadgeTone {
  if (['active', 'healthy', 'published', 'completed', 'approved'].includes(status))
    return 'positive'
  if (
    ['cooling', 'pending', 'pending_review', 'validating', 'queued', 'uncertain'].includes(status)
  ) {
    return 'warning'
  }
  if (['failed', 'invalid', 'unavailable', 'suspended'].includes(status)) return 'danger'
  if (['streaming', 'dispatching', 'admitted'].includes(status)) return 'info'
  return 'neutral'
}

export function StatusBadge({ status }: { status: string }) {
  return <Badge tone={statusTone(status)}>{statusLabels[status] ?? status}</Badge>
}
