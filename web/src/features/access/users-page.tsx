import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, KeyRound, LogOut, Pause, Play } from 'lucide-react'
import { useMemo, useState } from 'react'

import { accessApi, type UserAccount } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatTokens } from '@/lib/format'

import { AccessTabs } from './access-tabs'
import { MemberPasswordDialog } from './member-password-dialog'

export function UsersPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'access:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [passwordUser, setPasswordUser] = useState<UserAccount | null>(null)
  const [sessionUser, setSessionUser] = useState<UserAccount | null>(null)
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
  const revokeSessions = useMutation({
    mutationFn: (user: UserAccount) => accessApi.revokeUserSessions(user.id),
    onSuccess: () => setSessionUser(null),
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
              {row.original.role === 'member' && row.original.status !== 'pending_review' ? (
                <Button
                  size="sm"
                  variant="quiet"
                  icon={<KeyRound size={15} />}
                  onClick={() => setPasswordUser(row.original)}
                >
                  重置密码
                </Button>
              ) : null}
              {row.original.status === 'active' ? (
                <Button
                  size="sm"
                  variant="quiet"
                  icon={<LogOut size={15} />}
                  disabled={revokeSessions.isPending}
                  onClick={() => setSessionUser(row.original)}
                >
                  撤销会话
                </Button>
              ) : null}
            </div>
          ) : null,
      },
    ],
    [canWrite, review, revokeSessions.isPending],
  )

  return (
    <Page>
      <PageHeader title="成员与 API Key" />
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
          error={query.error ?? review.error ?? revokeSessions.error}
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
      <MemberPasswordDialog
        user={passwordUser}
        onOpenChange={(open) => !open && setPasswordUser(null)}
      />
      <ConfirmDialog
        open={sessionUser !== null}
        onOpenChange={(open) => !open && setSessionUser(null)}
        title="撤销活动会话"
        description={
          sessionUser?.id === session.userId
            ? '保留当前会话并撤销其他活动会话。'
            : `撤销 ${sessionUser?.displayName ?? ''} 的全部活动会话。`
        }
        confirmLabel="确认撤销"
        onConfirm={() => sessionUser && revokeSessions.mutate(sessionUser)}
        pending={revokeSessions.isPending}
        danger
      />
    </Page>
  )
}

const roleLabel = { administrator: '管理员', member: '成员' } as const
