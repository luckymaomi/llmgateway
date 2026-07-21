import { Check, CheckCircle2, Copy, RefreshCw } from 'lucide-react'
import { useState } from 'react'

import type { ActiveConfiguration } from '@/api'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { formatDateTime } from '@/lib/format'

import { completedRevisionTitle, type CompletedRevisionResult } from './revision-operation'

interface ActiveRevisionSummaryProps {
  configuration: ActiveConfiguration | undefined
  loading: boolean
  failed: boolean
  retrying: boolean
  onRetry: () => void
}

export function ActiveRevisionSummary({
  configuration,
  loading,
  failed,
  retrying,
  onRetry,
}: ActiveRevisionSummaryProps) {
  const [copyOutcome, setCopyOutcome] = useState<{
    revisionId: string
    state: 'copied' | 'failed'
  }>()

  if (loading) {
    return (
      <div className="revision-bar" aria-label="当前生效配置" role="status">
        <RevisionFact label="当前生效" value="正在读取" />
        <RevisionFact label="修订 ID" value="正在读取" code />
        <RevisionFact label="生效时间" value="正在读取" />
      </div>
    )
  }

  if (failed) {
    return (
      <div className="revision-bar revision-bar--error" aria-label="当前生效配置" role="alert">
        <RevisionFact label="当前生效" value="状态无法确认" />
        <RevisionFact label="原因" value="无法读取当前生效配置" />
        <Button
          size="sm"
          variant="secondary"
          icon={<RefreshCw size={15} />}
          disabled={retrying}
          onClick={onRetry}
        >
          {retrying ? '正在重新读取' : '重新读取'}
        </Button>
      </div>
    )
  }

  const revisionId = configuration?.revisionId ?? null
  const currentCopyState = copyOutcome?.revisionId === revisionId ? copyOutcome.state : undefined
  return (
    <div className="revision-bar" aria-label="当前生效配置">
      <RevisionFact
        label="当前生效"
        value={revisionId ? '版本 ' + String(configuration?.sequence ?? 0) : '尚未发布'}
      />
      <div>
        <span>修订 ID</span>
        {revisionId ? (
          <div className="revision-bar__revision-id">
            <code title={revisionId}>{shortRevisionId(revisionId)}</code>
            <IconButton
              label={currentCopyState === 'copied' ? '已复制修订 ID' : '复制修订 ID'}
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(revisionId)
                  setCopyOutcome({ revisionId, state: 'copied' })
                } catch {
                  setCopyOutcome({ revisionId, state: 'failed' })
                }
              }}
            >
              {currentCopyState === 'copied' ? <Check size={15} /> : <Copy size={15} />}
            </IconButton>
          </div>
        ) : (
          <strong>无</strong>
        )}
      </div>
      <RevisionFact
        label="生效时间"
        value={revisionId ? formatDateTime(configuration?.updatedAt ?? undefined) : '未生效'}
      />
      {currentCopyState === 'failed' ? (
        <span className="revision-bar__copy-error" role="alert">
          浏览器未允许复制修订 ID。
        </span>
      ) : null}
    </div>
  )
}

export function CompletedRevisionOperation({ result }: { result: CompletedRevisionResult }) {
  const requestId = result.kind === 'capture' ? undefined : result.operation.requestId
  const updatedAt =
    result.kind === 'capture' ? result.revision.createdAt : result.operation.updatedAt
  return (
    <section className="operation-panel" aria-live="polite" aria-label="操作结果">
      <div className="operation-panel__icon">
        <CheckCircle2 size={22} />
      </div>
      <div className="operation-panel__body">
        <div className="operation-panel__heading">
          <strong>{completedMessage(result)}</strong>
          <StatusBadge status="completed" />
        </div>
        <div className="operation-panel__facts">
          {requestId ? <span>Request ID：{requestId}</span> : null}
          <span>完成：{formatDateTime(updatedAt)}</span>
        </div>
      </div>
    </section>
  )
}

function completedMessage(result: CompletedRevisionResult): string {
  return result.kind === 'capture'
    ? '已捕获配置版本 ' + String(result.revision.sequence)
    : completedRevisionTitle(result)
}

function RevisionFact({
  label,
  value,
  code = false,
}: {
  label: string
  value: string
  code?: boolean
}) {
  return (
    <div>
      <span>{label}</span>
      {code ? <code>{value}</code> : <strong>{value}</strong>}
    </div>
  )
}

function shortRevisionId(revisionId: string): string {
  return revisionId.slice(0, 8) + '…' + revisionId.slice(-4)
}
