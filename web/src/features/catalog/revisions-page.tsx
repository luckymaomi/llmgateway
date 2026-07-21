import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useRef, useState } from 'react'
import { z } from 'zod'

import { ApiProblem, catalogApi, type ConfigurationRevision } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import {
  clearPendingConfigurationOperation,
  loadPendingConfigurationOperation,
  storePendingConfigurationOperation,
} from '@/app/pending-operations'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { FormProblem } from '@/features/auth/form-problem'
import { useListSearch } from '@/hooks/use-list-search'

import { CatalogTabs } from './catalog-tabs'
import { completedRevisionTitle, type CompletedRevisionResult } from './revision-operation'
import { ActiveRevisionSummary, CompletedRevisionOperation } from './revision-presentation'
import { RevisionTable, type RevisionAction } from './revision-table'

type Submission =
  | { kind: 'capture'; idempotencyKey: string }
  | { kind: 'validate'; revisionId: string }
  | {
      kind: 'publish' | 'rollback'
      revisionId: string
      expectedActiveVersion: number
      idempotencyKey: string
    }
const pendingSubmissionSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('capture'), idempotencyKey: z.string().uuid() }),
  z.object({
    kind: z.enum(['publish', 'rollback']),
    revisionId: z.string().uuid(),
    expectedActiveVersion: z.number().int().nonnegative(),
    idempotencyKey: z.string().uuid(),
  }),
])

export function RevisionsPage() {
  const session = useSession()
  const canPublish = hasCapability(session, 'revisions:publish')
  const { state, setPage } = useListSearch()
  const queryClient = useQueryClient()
  const [completed, setCompleted] = useState<CompletedRevisionResult | null>(null)
  const [uncertain, setUncertain] = useState<Submission | undefined>(() =>
    readPendingSubmission(session.userId),
  )
  const [persistenceFailed, setPersistenceFailed] = useState(false)
  const requestInFlight = useRef(false)
  const query = useQuery({
    queryKey: ['configuration-revisions', state.page, state.pageSize],
    queryFn: ({ signal }) =>
      catalogApi.revisions({ page: state.page, pageSize: state.pageSize }, signal),
    placeholderData: keepPreviousData,
  })
  const active = useQuery({
    queryKey: ['configuration-active'],
    queryFn: ({ signal }) => catalogApi.activeConfiguration(signal),
  })
  const action = useMutation({
    async mutationFn(submission: Submission): Promise<CompletedRevisionResult> {
      if (submission.kind === 'capture') {
        return {
          kind: 'capture',
          revision: await catalogApi.captureRevision(submission.idempotencyKey),
        }
      }
      if (submission.kind === 'validate') {
        return {
          kind: 'validate',
          operation: await catalogApi.validateRevision(submission.revisionId),
        }
      }
      const operation =
        submission.kind === 'publish'
          ? await catalogApi.publishRevision(
              submission.revisionId,
              submission.expectedActiveVersion,
              submission.idempotencyKey,
            )
          : await catalogApi.rollbackRevision(
              submission.revisionId,
              submission.expectedActiveVersion,
              submission.idempotencyKey,
            )
      return { kind: submission.kind, operation }
    },
    async onSuccess(result) {
      requestInFlight.current = false
      clearPendingConfigurationOperation(session.userId)
      setUncertain(undefined)
      setCompleted(result)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['configuration-revisions'] }),
        queryClient.invalidateQueries({ queryKey: ['configuration-active'] }),
      ])
    },
    onError(error, submission) {
      requestInFlight.current = false
      if (isUnknownOutcome(error) && submission.kind !== 'validate') {
        setUncertain(submission)
      } else {
        clearPendingConfigurationOperation(session.userId)
        setUncertain(undefined)
      }
      void queryClient.invalidateQueries({ queryKey: ['configuration-active'] })
    },
  })

  function run(submission: Submission): void {
    if (requestInFlight.current || uncertain) return
    if (
      submission.kind !== 'validate' &&
      !storePendingConfigurationOperation(session.userId, submission)
    ) {
      setPersistenceFailed(true)
      return
    }
    setPersistenceFailed(false)
    setCompleted(null)
    setUncertain(undefined)
    requestInFlight.current = true
    action.mutate(submission)
  }

  function retryUncertain(): void {
    if (!uncertain || requestInFlight.current) return
    requestInFlight.current = true
    action.mutate(uncertain)
  }

  function runRevisionAction(revision: ConfigurationRevision, kind: RevisionAction): void {
    if (kind === 'validate') {
      run({ kind, revisionId: revision.id })
      return
    }
    run({
      kind,
      revisionId: revision.id,
      expectedActiveVersion: active.data?.version ?? 0,
      idempotencyKey: crypto.randomUUID(),
    })
  }

  const actionUnavailable = action.isPending || Boolean(uncertain)
  return (
    <Page>
      <PageHeader
        title="Provider 与模型"
        description="上游端点、模型能力与可发布配置"
        actions={
          canPublish ? (
            <Button
              icon={<Plus size={16} />}
              disabled={actionUnavailable}
              onClick={() => run({ kind: 'capture', idempotencyKey: crypto.randomUUID() })}
            >
              捕获当前配置
            </Button>
          ) : null
        }
      />
      <CatalogTabs />
      <ActiveRevisionSummary
        configuration={active.data}
        loading={active.isLoading}
        failed={active.isError}
        retrying={active.isFetching}
        onRetry={() => void active.refetch()}
      />
      <PageSection>
        {uncertain ? (
          <div className="inline-problem" role="alert">
            <strong>操作结果暂时无法确认。</strong>
            <span>请重试原操作；系统会使用同一幂等键对账。</span>
            <Button disabled={action.isPending} onClick={retryUncertain}>
              {action.isPending ? '正在确认' : '重试原操作'}
            </Button>
          </div>
        ) : (
          <>
            {persistenceFailed ? (
              <div className="inline-problem" role="alert">
                浏览器无法保存待确认操作，本次未提交。请允许当前标签页使用会话存储后重试。
              </div>
            ) : null}
            <FormProblem error={action.error} />
          </>
        )}
        <RevisionTable
          revisions={query.data?.items ?? []}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          canPublish={canPublish}
          actionUnavailable={actionUnavailable}
          activeUnavailable={active.isLoading || active.isError}
          onAction={runRevisionAction}
        />
      </PageSection>
      <DialogFrame
        open={completed !== null}
        onOpenChange={(open) => {
          if (!open) setCompleted(null)
        }}
        title={completed ? completedRevisionTitle(completed) : '配置结果'}
      >
        {completed ? <CompletedRevisionOperation result={completed} /> : null}
      </DialogFrame>
    </Page>
  )
}

function readPendingSubmission(userId: string): Submission | undefined {
  const parsed = pendingSubmissionSchema.safeParse(loadPendingConfigurationOperation(userId))
  if (parsed.success) return parsed.data
  clearPendingConfigurationOperation(userId)
  return undefined
}

function isUnknownOutcome(error: unknown): boolean {
  return (
    error instanceof ApiProblem &&
    (error.code === 'operation_outcome_unknown' || error.code === 'network_unavailable')
  )
}
