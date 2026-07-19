import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Edit3, FlaskConical, Plus, Power } from 'lucide-react'
import { useMemo, useState } from 'react'

import { catalogApi, type Credential, type OperationSnapshot } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { OperationPanel } from '@/components/operations/operation-panel'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatNumber, formatPercent } from '@/lib/format'

import { CredentialForm } from './credential-form'

export function CredentialsPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'credentials:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<Credential | null | 'create'>(null)
  const [testing, setTesting] = useState<Credential | null>(null)
  const [operation, setOperation] = useState<OperationSnapshot | null>(null)
  const query = useQuery({
    queryKey: ['credentials', state],
    queryFn: ({ signal }) => catalogApi.credentials(state, signal),
    placeholderData: keepPreviousData,
  })
  const toggle = useMutation({
    mutationFn: (credential: Credential) =>
      catalogApi.setCredentialEnabled(credential.id, credential.status !== 'active'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['credentials'] }),
  })
  const test = useMutation({
    mutationFn: ({
      credential,
      mode,
    }: {
      credential: Credential
      mode: 'connection' | 'generation'
    }) => catalogApi.testCredential(credential.id, mode),
    onSuccess: setOperation,
  })
  const columns = useMemo<ColumnDef<Credential, unknown>[]>(
    () => [
      {
        accessorKey: 'label',
        header: '凭据',
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
        accessorKey: 'authorizedModels',
        header: '授权模型',
        cell: ({ row }) => `${row.original.authorizedModels.length} 个`,
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
        header: '近期成功率',
        cell: ({ row }) => formatPercent(row.original.recentSuccessRate),
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
            <div className="row-actions" onClick={(event) => event.stopPropagation()}>
              <IconButton label="编辑凭据" onClick={() => setEditing(row.original)}>
                <Edit3 size={16} />
              </IconButton>
              <IconButton
                label="测试凭据"
                onClick={() => {
                  setTesting(row.original)
                  setOperation(null)
                }}
              >
                <FlaskConical size={16} />
              </IconButton>
              <IconButton
                label={row.original.status === 'active' ? '停用凭据' : '启用凭据'}
                disabled={toggle.isPending}
                onClick={() => toggle.mutate(row.original)}
              >
                <Power size={16} />
              </IconButton>
            </div>
          ) : null,
      },
    ],
    [canWrite, toggle],
  )

  return (
    <Page>
      <PageHeader
        title="上游凭据池"
        description="凭据授权、限制、健康与冷却"
        actions={
          canWrite ? (
            <Button icon={<Plus size={16} />} onClick={() => setEditing('create')}>
              添加凭据
            </Button>
          ) : null
        }
      />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索凭据"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'active', label: '可用' },
            { value: 'cooling', label: '冷却中' },
            { value: 'disabled', label: '已停用' },
          ]}
        />
        <DataTable
          ariaLabel="上游凭据列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(credential) => credential.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error ?? toggle.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的凭据"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          onRowClick={canWrite ? (credential) => setEditing(credential) : undefined}
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
                {credential.authorizedModels.length} 个模型 · 成功率{' '}
                {formatPercent(credential.recentSuccessRate)}
              </span>
            </div>
          )}
        />
      </PageSection>
      <CredentialForm
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        {...(editing && editing !== 'create' ? { credential: editing } : {})}
      />
      <DialogFrame
        open={testing !== null}
        onOpenChange={(open) => {
          if (!open) {
            setTesting(null)
            setOperation(null)
          }
        }}
        title={`测试 ${testing?.label ?? ''}`}
        description="生成测试会消耗对应资源域的上游额度"
        {...(!operation && testing
          ? {
              footer: (
                <>
                  <Button
                    variant="secondary"
                    disabled={test.isPending}
                    onClick={() => test.mutate({ credential: testing, mode: 'connection' })}
                  >
                    连接探测
                  </Button>
                  <Button
                    disabled={test.isPending}
                    onClick={() => test.mutate({ credential: testing, mode: 'generation' })}
                  >
                    生成测试
                  </Button>
                </>
              ),
            }
          : {})}
      >
        {operation ? (
          <OperationPanel initial={operation} />
        ) : (
          <div className="test-choice">
            <FlaskConical size={24} />
            <span>选择测试方式</span>
          </div>
        )}
      </DialogFrame>
    </Page>
  )
}
