import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Edit3, PlugZap, Plus, Power } from 'lucide-react'
import { useMemo, useState } from 'react'
import { z } from 'zod'

import { ApiProblem, catalogApi, type Credential, type CredentialModelBinding } from '@/api'
import {
  clearPendingCredentialOperation,
  loadPendingCredentialOperation,
} from '@/app/pending-operations'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { IconButton } from '@/components/ui/icon-button'
import { FormProblem } from '@/features/auth/form-problem'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatDuration, formatNumber, formatPercent } from '@/lib/format'

import { CredentialForm } from './credential-form'
import { CredentialEditForm } from './credential-edit-form'
import { CredentialProbeDialog } from './credential-probe-dialog'

const pendingCredentialSchema = z.object({
  idempotencyKey: z.string().uuid(),
  providerId: z.string().uuid(),
  label: z.string().min(1),
  resourceDomain: z.enum(['free', 'professional']),
  modelBindings: z
    .array(
      z.object({
        modelId: z.string().uuid(),
        priority: z.number().int().min(0).max(1000),
        weight: z.number().int().min(1).max(1000),
      }),
    )
    .min(1),
})
type PendingCredential = z.infer<typeof pendingCredentialSchema>
type StatusOperation = {
  credentialId: string
  enabled: boolean
  expectedUpdatedAt: string
  idempotencyKey: string
}

export function CredentialsPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'credentials:write')
  const queryClient = useQueryClient()
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [creating, setCreating] = useState(false)
  const [discardingPending, setDiscardingPending] = useState(false)
  const [editing, setEditing] = useState<Credential>()
  const [probeTarget, setProbeTarget] = useState<Credential>()
  const [uncertainStatus, setUncertainStatus] = useState<StatusOperation>()
  const [pendingOperation, setPendingOperation] = useState<PendingCredential | undefined>(() =>
    readPendingCredentialOperation(session.userId),
  )
  const query = useQuery({
    queryKey: ['credentials', state],
    queryFn: ({ signal }) => catalogApi.credentials(state, signal),
    placeholderData: keepPreviousData,
  })
  const toggle = useMutation({
    mutationFn: (operation: StatusOperation) =>
      catalogApi.setCredentialEnabled(
        operation.credentialId,
        operation.enabled,
        operation.expectedUpdatedAt,
        operation.idempotencyKey,
      ),
    onSuccess() {
      setUncertainStatus(undefined)
      return queryClient.invalidateQueries({ queryKey: ['credentials'] })
    },
    onError(error, operation) {
      setUncertainStatus(isUnknownOutcome(error) ? operation : undefined)
      return queryClient.invalidateQueries({ queryKey: ['credentials'] })
    },
  })
  const columns = useMemo<ColumnDef<Credential, unknown>[]>(
    () => [
      {
        accessorKey: 'label',
        header: 'API Key',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.label}</strong>
            <small className="table-subline">{row.original.maskedSecret}</small>
          </div>
        ),
      },
      { accessorKey: 'providerName', header: 'Provider' },
      {
        accessorKey: 'resourceDomain',
        header: '资源域',
        cell: ({ row }) => (
          <Badge tone={row.original.resourceDomain === 'free' ? 'positive' : 'info'}>
            {row.original.resourceDomain === 'free' ? '免费' : '专业'}
          </Badge>
        ),
      },
      {
        accessorKey: 'modelBindings',
        header: '模型路由',
        cell: ({ row }) => `${row.original.modelBindings.length} 个`,
      },
      {
        accessorKey: 'rpmLimit',
        header: 'RPM',
        cell: ({ row }) => (row.original.rpmLimit ? formatNumber(row.original.rpmLimit) : '未知'),
      },
      {
        accessorKey: 'concurrencyLimit',
        header: '并发',
        cell: ({ row }) => row.original.concurrencyLimit ?? '未知',
      },
      {
        accessorKey: 'recentSuccessRate',
        header: '24 小时质量',
        cell: ({ row }) => (
          <div>
            <strong>{formatPercent(row.original.recentSuccessRate)}</strong>
            <small className="table-subline">
              {row.original.lastCheckedAt
                ? `P95 ${formatDuration(row.original.totalLatencyP95Ms)}`
                : '暂无请求'}
            </small>
          </div>
        ),
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        accessorKey: 'cooldownUntil',
        header: '冷却到期',
        cell: ({ row }) => formatDateTime(row.original.cooldownUntil),
      },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) =>
          canWrite ? (
            <CredentialActions
              credential={row.original}
              disabled={toggle.isPending || Boolean(uncertainStatus)}
              onEdit={setEditing}
              onProbe={(credential) => {
                setProbeTarget(credential)
              }}
              onToggle={(credential) =>
                toggle.mutate({
                  credentialId: credential.id,
                  enabled: credential.status !== 'active',
                  expectedUpdatedAt: credential.updatedAt,
                  idempotencyKey: crypto.randomUUID(),
                })
              }
            />
          ) : null,
      },
    ],
    [canWrite, toggle, uncertainStatus],
  )
  const reconciledCredential = query.data?.items.find(
    (credential) =>
      credential.providerId === pendingOperation?.providerId &&
      credential.label === pendingOperation.label &&
      credential.resourceDomain === pendingOperation.resourceDomain &&
      equalModelBindings(credential.modelBindings, pendingOperation.modelBindings),
  )

  return (
    <Page>
      <PageHeader
        title="上游 API Key"
        actions={
          canWrite ? (
            <Button
              icon={<Plus size={16} />}
              disabled={Boolean(pendingOperation)}
              onClick={() => setCreating(true)}
            >
              添加上游 API Key
            </Button>
          ) : null
        }
      />
      <PageSection>
        {pendingOperation ? (
          <div className="inline-problem" role="alert">
            {reconciledCredential ? (
              <>
                <strong>已在持久列表中确认上次创建结果。</strong>
                <span>{reconciledCredential.label}</span>
                <Button
                  onClick={() => {
                    clearPendingCredentialOperation(session.userId)
                    setPendingOperation(undefined)
                  }}
                >
                  完成对账
                </Button>
              </>
            ) : (
              <>
                <strong>上次 API Key 创建结果仍待确认。</strong>
                <span>列表中暂未找到匹配记录；重新查询后再决定是否重新输入密钥。</span>
                <div className="row-actions">
                  <Button variant="secondary" onClick={() => void query.refetch()}>
                    重新查询
                  </Button>
                  <Button
                    disabled={query.isFetching || !query.data}
                    onClick={() => setDiscardingPending(true)}
                  >
                    重新输入
                  </Button>
                </div>
              </>
            )}
          </div>
        ) : null}
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索 API Key"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'active', label: '可用' },
            { value: 'cooling', label: '冷却中' },
            { value: 'disabled', label: '已停用' },
          ]}
        />
        {uncertainStatus ? (
          <div className="inline-problem" role="alert">
            <strong>启停结果暂时无法确认。</strong>
            <span>确认原操作会复用同一幂等键。</span>
            <Button disabled={toggle.isPending} onClick={() => toggle.mutate(uncertainStatus)}>
              {toggle.isPending ? '正在确认' : '确认原操作'}
            </Button>
          </div>
        ) : (
          <FormProblem error={toggle.error} />
        )}
        <DataTable
          ariaLabel="上游 API Key 列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(credential) => credential.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="还没有上游 API Key"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(credential) => (
            <div className="mobile-summary">
              <div>
                <strong>{credential.label}</strong>
                <StatusBadge status={credential.status} />
              </div>
              <span>
                {credential.providerName} · {credential.maskedSecret}
              </span>
              <span>
                {credential.modelBindings.length} 个模型 · 24 小时可用率{' '}
                {formatPercent(credential.recentSuccessRate)} · P95{' '}
                {formatDuration(credential.totalLatencyP95Ms)}
              </span>
            </div>
          )}
        />
      </PageSection>
      <CredentialForm open={creating} onOpenChange={setCreating} />
      {editing ? (
        <CredentialEditForm
          key={`${editing.id}:${editing.updatedAt}`}
          credential={editing}
          open
          onOpenChange={(open) => {
            if (!open) setEditing(undefined)
          }}
        />
      ) : null}
      {probeTarget ? (
        <CredentialProbeDialog
          credential={probeTarget}
          onOpenChange={(open) => {
            if (!open) setProbeTarget(undefined)
          }}
        />
      ) : null}
      <ConfirmDialog
        open={discardingPending}
        onOpenChange={setDiscardingPending}
        title="确认结束原操作对账"
        description="当前持久列表中没有匹配凭据。结束对账后，原密钥不会保留，需要重新输入并创建。"
        confirmLabel="结束并重新输入"
        onConfirm={() => {
          clearPendingCredentialOperation(session.userId)
          setPendingOperation(undefined)
          setDiscardingPending(false)
          setCreating(true)
        }}
      />
    </Page>
  )
}

function CredentialActions({
  credential,
  disabled,
  onEdit,
  onProbe,
  onToggle,
}: {
  credential: Credential
  disabled: boolean
  onEdit: (credential: Credential) => void
  onProbe: (credential: Credential) => void
  onToggle: (credential: Credential) => void
}) {
  return (
    <div className="row-actions" onClick={(event) => event.stopPropagation()}>
      <IconButton label="编辑 API Key" disabled={disabled} onClick={() => onEdit(credential)}>
        <Edit3 size={16} />
      </IconButton>
      <IconButton label="测试连接" disabled={disabled} onClick={() => onProbe(credential)}>
        <PlugZap size={16} />
      </IconButton>
      <IconButton
        label={credential.status === 'active' ? '停用 API Key' : '启用 API Key'}
        disabled={disabled}
        onClick={() => onToggle(credential)}
      >
        <Power size={16} />
      </IconButton>
    </div>
  )
}

function isUnknownOutcome(error: unknown): boolean {
  return (
    error instanceof ApiProblem &&
    (error.code === 'operation_outcome_unknown' || error.code === 'network_unavailable')
  )
}

function readPendingCredentialOperation(userId: string): PendingCredential | undefined {
  const parsed = pendingCredentialSchema.safeParse(loadPendingCredentialOperation(userId))
  if (parsed.success) return parsed.data
  clearPendingCredentialOperation(userId)
  return undefined
}

function equalModelBindings(
  left: CredentialModelBinding[],
  right: Array<Omit<CredentialModelBinding, 'modelName'>>,
): boolean {
  if (left.length !== right.length) return false
  const expected = new Map(right.map((binding) => [binding.modelId, binding]))
  return left.every((binding) => {
    const other = expected.get(binding.modelId)
    return other?.priority === binding.priority && other.weight === binding.weight
  })
}
