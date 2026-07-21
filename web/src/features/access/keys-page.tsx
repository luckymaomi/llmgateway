import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Plus, XCircle } from 'lucide-react'
import { useMemo, useState } from 'react'

import { accessApi, type GatewayKey } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { FormProblem } from '@/features/auth/form-problem'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { AccessTabs } from './access-tabs'
import { KeyForm } from './key-form'

export function KeysPage() {
  const session = useSession()
  const canRevoke = session.role === 'member' || hasCapability(session, 'access:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [creating, setCreating] = useState(false)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['gateway-keys', state],
    queryFn: ({ signal }) => accessApi.keys(state, signal),
    placeholderData: keepPreviousData,
  })
  const revoke = useMutation({
    mutationFn: accessApi.revokeKey,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['gateway-keys'] }),
    onError: () => queryClient.invalidateQueries({ queryKey: ['gateway-keys'] }),
  })
  const columns = useMemo<ColumnDef<GatewayKey, unknown>[]>(
    () => [
      {
        accessorKey: 'name',
        header: 'Key',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.name}</strong>
            <small className="table-subline">
              <code>{row.original.prefix}…</code>
            </small>
          </div>
        ),
      },
      ...(session.role === 'member'
        ? []
        : [{ accessorKey: 'ownerName', header: '所属用户' } as ColumnDef<GatewayKey, unknown>]),
      {
        accessorKey: 'authorizedModels',
        header: '模型授权',
        cell: ({ row }) => `${row.original.authorizedModels.length} 个`,
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        accessorKey: 'expiresAt',
        header: '到期',
        cell: ({ row }) => formatDateTime(row.original.expiresAt),
      },
      {
        accessorKey: 'lastUsedAt',
        header: '最近使用',
        cell: ({ row }) => formatDateTime(row.original.lastUsedAt),
      },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) =>
          canRevoke && row.original.status === 'active' ? (
            <Button
              size="sm"
              variant="quiet"
              icon={<XCircle size={15} />}
              disabled={revoke.isPending}
              onClick={() => revoke.mutate(row.original.id)}
            >
              撤销
            </Button>
          ) : null,
      },
    ],
    [canRevoke, revoke, session.role],
  )
  return (
    <Page>
      <PageHeader
        title={session.role === 'member' ? '我的网关 Key' : '用户与网关 Key'}
        description={
          session.role === 'member'
            ? '查看并撤销当前账号的调用凭据'
            : '查看用户调用凭据并撤销失效 Key'
        }
        actions={
          session.role === 'administrator' ? (
            <Button icon={<Plus size={16} />} onClick={() => setCreating(true)}>
              创建 Key
            </Button>
          ) : null
        }
      />
      <AccessTabs />
      <PageSection>
        <FormProblem error={revoke.error} />
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索 Key"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'active', label: '可用' },
            { value: 'revoked', label: '已撤销' },
            { value: 'expired', label: '已过期' },
          ]}
        />
        <DataTable
          ariaLabel="网关 Key 列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(key) => key.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的网关 Key"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(key) => (
            <div className="mobile-summary">
              <div>
                <strong>
                  <KeyRound size={15} />
                  {key.name}
                </strong>
                <StatusBadge status={key.status} />
              </div>
              <span>
                <code>{key.prefix}…</code> · {key.authorizedModels.length} 个模型
              </span>
              <span>{formatDateTime(key.lastUsedAt)}</span>
            </div>
          )}
        />
      </PageSection>
      {session.role === 'administrator' ? (
        <KeyForm open={creating} onOpenChange={setCreating} />
      ) : null}
    </Page>
  )
}
