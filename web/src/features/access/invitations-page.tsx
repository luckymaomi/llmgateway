import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, XCircle } from 'lucide-react'
import { useMemo, useState } from 'react'

import { accessApi, type Invitation } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { AccessTabs } from './access-tabs'
import { InvitationForm } from './invitation-form'

export function InvitationsPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'access:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [creating, setCreating] = useState(false)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['invitations', state],
    queryFn: ({ signal }) => accessApi.invitations(state, signal),
    placeholderData: keepPreviousData,
  })
  const revoke = useMutation({
    mutationFn: accessApi.revokeInvitation,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['invitations'] }),
  })
  const columns = useMemo<ColumnDef<Invitation, unknown>[]>(
    () => [
      {
        accessorKey: 'codePrefix',
        header: '邀请码',
        cell: ({ row }) => <code>{row.original.codePrefix}…</code>,
      },
      {
        accessorKey: 'role',
        header: '角色',
        cell: ({ row }) => (row.original.role === 'operator' ? '运维人员' : '成员'),
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        accessorKey: 'claimedBy',
        header: '领取人',
        cell: ({ row }) => row.original.claimedBy ?? '尚未领取',
      },
      {
        accessorKey: 'expiresAt',
        header: '到期',
        cell: ({ row }) => formatDateTime(row.original.expiresAt),
      },
      { accessorKey: 'createdBy', header: '创建人' },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) =>
          canWrite && row.original.status === 'issued' ? (
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
    [canWrite, revoke],
  )
  return (
    <Page>
      <PageHeader
        title="用户与网关 Key"
        description="邀请、审核、模型授权和调用凭据"
        actions={
          canWrite ? (
            <Button icon={<Plus size={16} />} onClick={() => setCreating(true)}>
              创建邀请
            </Button>
          ) : null
        }
      />
      <AccessTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索邀请"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'issued', label: '已签发' },
            { value: 'claimed', label: '已领取' },
            { value: 'revoked', label: '已撤销' },
          ]}
        />
        <DataTable
          ariaLabel="邀请列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(invitation) => invitation.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error ?? revoke.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的邀请"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(invitation) => (
            <div className="mobile-summary">
              <div>
                <code>{invitation.codePrefix}…</code>
                <StatusBadge status={invitation.status} />
              </div>
              <span>
                {invitation.role === 'operator' ? '运维人员' : '成员'} ·{' '}
                {formatDateTime(invitation.expiresAt)}
              </span>
            </div>
          )}
        />
      </PageSection>
      {canWrite ? <InvitationForm open={creating} onOpenChange={setCreating} /> : null}
    </Page>
  )
}
