import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Pause, Play } from 'lucide-react'
import { useMemo } from 'react'

import { accessApi, type UserAccount } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatTokens } from '@/lib/format'

import { AccessTabs } from './access-tabs'

export function UsersPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'access:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['users', state],
    queryFn: ({ signal }) => accessApi.users(state, signal),
    placeholderData: keepPreviousData,
  })
  const review = useMutation({
    mutationFn: ({
      user,
      decision,
    }: {
      user: UserAccount
      decision: 'approve' | 'suspend' | 'activate'
    }) => accessApi.reviewUser(user.id, decision),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['users'] }),
  })
  const columns = useMemo<ColumnDef<UserAccount, unknown>[]>(
    () => [
      {
        accessorKey: 'displayName',
        header: '用户',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.displayName}</strong>
            <small className="table-subline">{row.original.email}</small>
          </div>
        ),
      },
      { accessorKey: 'role', header: '角色', cell: ({ row }) => roleLabel[row.original.role] },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      { accessorKey: 'modelCount', header: '模型授权' },
      { accessorKey: 'keyCount', header: 'Key' },
      {
        accessorKey: 'quotaRemainingTokens',
        header: '剩余额度',
        cell: ({ row }) => formatTokens(row.original.quotaRemainingTokens),
      },
      {
        accessorKey: 'lastActiveAt',
        header: '最近活动',
        cell: ({ row }) => formatDateTime(row.original.lastActiveAt),
      },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) =>
          canWrite ? (
            <div className="row-actions">
              {row.original.status === 'pending_review' ? (
                <Button
                  size="sm"
                  variant="quiet"
                  icon={<Check size={15} />}
                  disabled={review.isPending}
                  onClick={() => review.mutate({ user: row.original, decision: 'approve' })}
                >
                  批准
                </Button>
              ) : null}
              {row.original.status === 'active' ? (
                <Button
                  size="sm"
                  variant="quiet"
                  icon={<Pause size={15} />}
                  disabled={review.isPending}
                  onClick={() => review.mutate({ user: row.original, decision: 'suspend' })}
                >
                  停用
                </Button>
              ) : null}
              {row.original.status === 'suspended' ? (
                <Button
                  size="sm"
                  variant="quiet"
                  icon={<Play size={15} />}
                  disabled={review.isPending}
                  onClick={() => review.mutate({ user: row.original, decision: 'activate' })}
                >
                  启用
                </Button>
              ) : null}
            </div>
          ) : null,
      },
    ],
    [canWrite, review],
  )

  return (
    <Page>
      <PageHeader title="用户与网关 Key" description="邀请、审核、模型授权和调用凭据" />
      <AccessTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索用户"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'pending_review', label: '待审核' },
            { value: 'active', label: '可用' },
            { value: 'suspended', label: '已停用' },
          ]}
        />
        <DataTable
          ariaLabel="用户列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(user) => user.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error ?? review.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的用户"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(user) => (
            <div className="mobile-summary">
              <div>
                <strong>{user.displayName}</strong>
                <StatusBadge status={user.status} />
              </div>
              <span>{user.email}</span>
              <span>
                {user.modelCount} 个模型 · {user.keyCount} 个 Key ·{' '}
                {formatTokens(user.quotaRemainingTokens)}
              </span>
            </div>
          )}
        />
      </PageSection>
    </Page>
  )
}

const roleLabel = { administrator: '管理员', operator: '运维人员', member: '成员' } as const
