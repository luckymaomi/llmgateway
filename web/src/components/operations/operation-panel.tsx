import { useMutation, useQuery } from '@tanstack/react-query'
import { Ban, CheckCircle2, CircleAlert, Clock3, LoaderCircle } from 'lucide-react'

import { operationsApi, type OperationPhase, type OperationSnapshot } from '@/api'
import { formatDateTime } from '@/lib/format'

import { Button } from '../ui/button'
import { StatusBadge } from '../ui/badge'

const terminalPhases: OperationPhase[] = ['completed', 'failed', 'canceled', 'uncertain']

export function OperationPanel({ initial }: { initial: OperationSnapshot }) {
  const query = useQuery({
    queryKey: ['operation', initial.id],
    queryFn: ({ signal }) => operationsApi.operation(initial.id, signal),
    initialData: initial,
    refetchInterval(queryState) {
      const phase = queryState.state.data?.phase
      return phase && terminalPhases.includes(phase) ? false : 1_000
    },
  })
  const cancel = useMutation({
    mutationFn: () => operationsApi.cancelOperation(initial.id),
    onSuccess(snapshot) {
      query.refetch().catch(() => undefined)
      return snapshot
    },
  })
  const operation = query.data
  const icon = operationIcon(operation.phase)

  return (
    <section className="operation-panel" aria-live="polite" aria-label="操作进度">
      <div className="operation-panel__icon">{icon}</div>
      <div className="operation-panel__body">
        <div className="operation-panel__heading">
          <strong>{operation.step}</strong>
          <StatusBadge status={operation.phase} />
        </div>
        <div className="operation-panel__facts">
          <span>Request ID：{operation.requestId}</span>
          <span>更新：{formatDateTime(operation.updatedAt)}</span>
        </div>
        {operation.progress !== undefined ? (
          <div className="progress" aria-label="操作完成度">
            <progress max={100} value={operation.progress} />
            <span>{Math.round(operation.progress)}%</span>
          </div>
        ) : null}
        {operation.error ? (
          <div className="inline-problem" role="alert">
            <strong>{operation.error.message}</strong>
            <span>{operation.error.code}</span>
            {operation.error.stage ? <span>阶段：{operation.error.stage}</span> : null}
          </div>
        ) : null}
      </div>
      {operation.canCancel && !terminalPhases.includes(operation.phase) ? (
        <Button
          type="button"
          variant="secondary"
          size="sm"
          icon={<Ban size={15} />}
          disabled={cancel.isPending}
          onClick={() => cancel.mutate()}
        >
          {cancel.isPending ? '取消中' : '取消'}
        </Button>
      ) : null}
    </section>
  )
}

function operationIcon(phase: OperationPhase) {
  if (phase === 'completed') return <CheckCircle2 size={22} />
  if (phase === 'failed' || phase === 'uncertain') return <CircleAlert size={22} />
  if (phase === 'canceled') return <Ban size={22} />
  if (phase === 'queued' || phase === 'waiting') return <Clock3 size={22} />
  return <LoaderCircle className="spin" size={22} />
}
