import { AlertCircle, Inbox, LoaderCircle, RefreshCw } from 'lucide-react'
import type { ReactNode } from 'react'

import { ApiProblem } from '@/api'

import { Button } from './button'

export function LoadingState({ label = '正在加载' }: { label?: string }) {
  return (
    <div className="state" role="status" aria-live="polite">
      <LoaderCircle className="spin" size={24} />
      <span>{label}</span>
    </div>
  )
}

export function EmptyState({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <div className="state">
      <Inbox size={26} />
      <strong>{title}</strong>
      {action}
    </div>
  )
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const problem =
    error instanceof ApiProblem
      ? error
      : new ApiProblem({
          status: 500,
          code: 'unexpected_error',
          message: error instanceof Error ? error.message : '请求未完成',
          retryable: true,
        })
  return (
    <div className="state state--error" role="alert">
      <AlertCircle size={26} />
      <strong>{problem.message}</strong>
      <div className="state__facts">
        <code>{problem.code}</code>
        {problem.stage ? <span>阶段：{problem.stage}</span> : null}
        {problem.requestId ? <span>Request ID：{problem.requestId}</span> : null}
      </div>
      {onRetry ? (
        <Button
          type="button"
          variant="secondary"
          size="sm"
          icon={<RefreshCw size={15} />}
          onClick={onRetry}
        >
          重试
        </Button>
      ) : null}
    </div>
  )
}
