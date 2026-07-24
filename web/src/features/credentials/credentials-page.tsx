import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Archive, FlaskConical, Pencil, Play, Plus, Power } from 'lucide-react'
import { useMemo, useState } from 'react'

import { catalogApi, type Credential, type CredentialStatus } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import {
  RowActionItem,
  RowActionMenu,
  RowActionSeparator,
  TableAction,
} from '@/components/data-table/row-actions'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { FormProblem } from '@/features/auth/form-problem'
import { formatDateTime, formatNumber } from '@/lib/format'

import { CredentialBatchForm } from './credential-batch-form'
import { CredentialForm } from './credential-form'
import { probeErrorLabel } from './credential-probe-copy'
import { CredentialProbeDialog } from './credential-probe-dialog'

export function CredentialsPage() {
  const queryClient = useQueryClient()
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<Credential | null>(null)
  const [probing, setProbing] = useState<Credential | null>(null)
  const [statusTarget, setStatusTarget] = useState<{
    credential: Credential
    status: CredentialStatus
  } | null>(null)
  const [retiring, setRetiring] = useState<Credential | null>(null)
  const query = useQuery({
    queryKey: ['credentials'],
    queryFn: ({ signal }) => catalogApi.credentials(true, signal),
  })
  const statusMutation = useMutation({
    mutationFn: ({ credential, status }: NonNullable<typeof statusTarget>) =>
      catalogApi.setCredentialStatus(
        credential.id,
        status,
        credential.updatedAt,
        crypto.randomUUID(),
      ),
    async onSuccess() {
      setStatusTarget(null)
      await queryClient.invalidateQueries({ queryKey: ['credentials'] })
    },
  })
  const retireMutation = useMutation({
    mutationFn: (credential: Credential) =>
      catalogApi.retireCredential(credential.id, credential.updatedAt, crypto.randomUUID()),
    async onSuccess() {
      setRetiring(null)
      await queryClient.invalidateQueries({ queryKey: ['credentials'] })
      await queryClient.invalidateQueries({ queryKey: ['resource-pools'] })
    },
  })
  const columns = useMemo<ColumnDef<Credential, unknown>[]>(
    () => [
      {
        accessorKey: 'name',
        header: '上游 API Key',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.name}</strong>
            <small className="table-subline">{row.original.providerName}</small>
          </div>
        ),
      },
      { accessorKey: 'resourcePoolName', header: '资源池' },
      {
        id: 'models',
        header: '模型',
        cell: ({ row }) => row.original.modelBindings.map((item) => item.modelName).join('、'),
      },
      {
        id: 'limits',
        header: 'RPM / TPM / 并发',
        cell: ({ row }) =>
          `${limit(row.original.rpmLimit)} / ${limit(row.original.tpmLimit)} / ${limit(row.original.concurrencyLimit)}`,
        meta: { align: 'right' },
      },
      {
        id: 'probe',
        header: '最近探测',
        meta: { align: 'center' },
        cell: ({ row }) =>
          row.original.lastProbeAt ? (
            <div className="table-status-cell">
              <StatusBadge status={row.original.lastProbeStatus ?? 'unknown'} />
              <small className="table-subline">
                {row.original.lastProbeStatus === 'succeeded' &&
                row.original.lastProbeLatencyMs !== undefined
                  ? `${formatNumber(row.original.lastProbeLatencyMs)} ms`
                  : probeErrorLabel(row.original.lastProbeErrorKind)}
              </small>
              <small className="table-subline">{formatDateTime(row.original.lastProbeAt)}</small>
            </div>
          ) : (
            '未探测'
          ),
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
        meta: { align: 'center' },
      },
      {
        id: 'actions',
        header: '操作',
        meta: { align: 'center' },
        cell: ({ row }) =>
          row.original.status !== 'retired' ? (
            <div className="row-actions row-actions--center">
              <TableAction
                label="测试"
                icon={<FlaskConical size={16} />}
                onClick={() => setProbing(row.original)}
              />
              <TableAction
                label="编辑"
                icon={<Pencil size={16} />}
                onClick={() => setEditing(row.original)}
              />
              <RowActionMenu>
                {row.original.status === 'disabled' ? (
                  <RowActionItem
                    icon={<Play size={15} />}
                    onSelect={() => setStatusTarget({ credential: row.original, status: 'active' })}
                  >
                    启用 Key
                  </RowActionItem>
                ) : (
                  <RowActionItem
                    icon={<Power size={15} />}
                    onSelect={() =>
                      setStatusTarget({ credential: row.original, status: 'disabled' })
                    }
                  >
                    停用 Key
                  </RowActionItem>
                )}
                {row.original.status === 'disabled' ? (
                  <>
                    <RowActionSeparator />
                    <RowActionItem
                      icon={<Archive size={15} />}
                      danger
                      onSelect={() => setRetiring(row.original)}
                    >
                      退役 Key
                    </RowActionItem>
                  </>
                ) : null}
              </RowActionMenu>
            </div>
          ) : null,
      },
    ],
    [],
  )

  return (
    <Page>
      <PageHeader
        title="上游 API Key"
        actions={
          <Button
            icon={<Plus size={16} />}
            data-onboarding="create-provider-key"
            onClick={() => setAdding(true)}
          >
            添加上游 API Key
          </Button>
        }
      />
      <PageSection>
        <FormProblem error={statusMutation.error ?? retireMutation.error} />
        <DataTable
          ariaLabel="上游 API Key 列表"
          data={query.data ?? []}
          columns={columns}
          getRowId={(item) => item.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="还没有上游 API Key"
          page={1}
          pageSize={Math.max(query.data?.length ?? 0, 1)}
          total={query.data?.length ?? 0}
          onPageChange={() => undefined}
        />
      </PageSection>
      {editing ? (
        <CredentialForm
          credential={editing}
          open
          onOpenChange={(open) => !open && setEditing(null)}
        />
      ) : null}
      <CredentialBatchForm open={adding} onOpenChange={setAdding} />
      {probing ? (
        <CredentialProbeDialog
          credential={probing}
          onOpenChange={(open) => !open && setProbing(null)}
        />
      ) : null}
      <ConfirmDialog
        open={statusTarget !== null}
        onOpenChange={(open) => !open && setStatusTarget(null)}
        title="更改上游 API Key 状态"
        description="状态提交后会直接影响新请求的候选资格。"
        confirmLabel="确认"
        pending={statusMutation.isPending}
        onConfirm={() => statusTarget && statusMutation.mutate(statusTarget)}
      />
      <ConfirmDialog
        open={retiring !== null}
        onOpenChange={(open) => !open && setRetiring(null)}
        title="退役上游 API Key"
        description="退役后 secret 将退出调度资格，历史请求仍保留脱敏引用。"
        confirmLabel="确认退役"
        pending={retireMutation.isPending}
        danger
        onConfirm={() => retiring && retireMutation.mutate(retiring)}
      />
    </Page>
  )
}

function limit(value: number | undefined) {
  return value === undefined ? '不限' : formatNumber(value)
}
