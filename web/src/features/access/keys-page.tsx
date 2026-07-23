import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CirclePlay, Plus, RotateCw, XCircle } from 'lucide-react'
import { useMemo, useState } from 'react'

import { accessApi, type GatewayKey } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { FormProblem } from '@/features/auth/form-problem'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { GatewayKeyTestDialog } from './gateway-key-test-dialog'
import { KeyForm } from './key-form'
import { KeyReplacementDialog } from './key-replacement-dialog'

export function KeysPage() {
  const session = useSession()
  const canRevoke = hasCapability(session, 'keys:write')
  const canTest = hasCapability(session, 'api-key:test')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [creating, setCreating] = useState(false)
  const [replacementKey, setReplacementKey] = useState<GatewayKey | null>(null)
  const [revokeKey, setRevokeKey] = useState<GatewayKey | null>(null)
  const [testKey, setTestKey] = useState<GatewayKey | null>(null)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['gateway-keys', state],
    queryFn: ({ signal }) => accessApi.keys(state, signal),
    placeholderData: keepPreviousData,
  })
  const revoke = useMutation({
    mutationFn: accessApi.revokeKey,
    onSuccess: () => {
      setRevokeKey(null)
      return queryClient.invalidateQueries({ queryKey: ['gateway-keys'] })
    },
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
          row.original.status === 'active' && (canRevoke || canTest) ? (
            <div className="row-actions">
              {canTest ? (
                <Button
                  size="sm"
                  variant="quiet"
                  icon={<CirclePlay size={15} />}
                  data-onboarding="test-api-key"
                  disabled={revoke.isPending}
                  onClick={() => setTestKey(row.original)}
                >
                  测试请求
                </Button>
              ) : null}
              {canRevoke ? (
                <>
                  <Button
                    size="sm"
                    variant="quiet"
                    icon={<RotateCw size={15} />}
                    disabled={revoke.isPending}
                    onClick={() => setReplacementKey(row.original)}
                  >
                    更换
                  </Button>
                  <Button
                    size="sm"
                    variant="quiet"
                    icon={<XCircle size={15} />}
                    disabled={revoke.isPending}
                    onClick={() => setRevokeKey(row.original)}
                  >
                    撤销
                  </Button>
                </>
              ) : null}
            </div>
          ) : null,
      },
    ],
    [canRevoke, canTest, revoke, session.role],
  )
  return (
    <Page>
      <PageHeader
        title="API 密钥"
        actions={
          canRevoke ? (
            <Button
              icon={<Plus size={16} />}
              data-onboarding="create-api-key"
              onClick={() => setCreating(true)}
            >
              创建 API 密钥
            </Button>
          ) : null
        }
      />
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
          ariaLabel="API 密钥列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(key) => key.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="还没有 API 密钥"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
        />
      </PageSection>
      {canRevoke ? <KeyForm open={creating} onOpenChange={setCreating} /> : null}
      <KeyReplacementDialog
        gatewayKey={replacementKey}
        onOpenChange={(open) => !open && setReplacementKey(null)}
      />
      {testKey ? (
        <GatewayKeyTestDialog
          gatewayKey={testKey}
          onOpenChange={(open) => !open && setTestKey(null)}
        />
      ) : null}
      <ConfirmDialog
        open={revokeKey !== null}
        onOpenChange={(open) => !open && setRevokeKey(null)}
        title="撤销 API 密钥"
        description={`撤销 ${revokeKey?.name ?? ''} 后，使用该 API 密钥的请求将立即失败。`}
        confirmLabel="确认撤销"
        onConfirm={() => revokeKey && revoke.mutate(revokeKey.id)}
        pending={revoke.isPending}
        danger
      />
    </Page>
  )
}
