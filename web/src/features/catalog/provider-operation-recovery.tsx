import { ApiProblem } from '@/api'
import { Button } from '@/components/ui/button'

export function ProviderOperationRecovery({
  error,
  pending,
  onRetry,
}: {
  error: unknown
  pending: boolean
  onRetry: () => void
}) {
  const problem = error instanceof ApiProblem ? error : undefined
  return (
    <div className="form-problem" role="alert">
      <strong>操作结果尚未确认</strong>
      <span>系统没有自动重放。请使用原请求确认最终结果。</span>
      {problem?.requestId ? <span>Request ID：{problem.requestId}</span> : null}
      <Button type="button" variant="secondary" disabled={pending} onClick={onRetry}>
        {pending ? '正在重试原操作' : '重试原操作'}
      </Button>
    </div>
  )
}
