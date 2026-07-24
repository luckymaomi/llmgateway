import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Pencil, Play, Plus, Power, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'

import { accessApi, type UserAccount } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import {
  RowActionItem,
  RowActionMenu,
  RowActionSeparator,
  TableAction,
} from '@/components/data-table/row-actions'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { FormProblem } from '@/features/auth/form-problem'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { MemberForm } from './member-form'
import { MemberPasswordDialog } from './member-password-dialog'

export function UsersPage() {
  const queryClient = useQueryClient()
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<UserAccount | null>(null)
  const [passwordMember, setPasswordMember] = useState<UserAccount | null>(null)
  const [deleting, setDeleting] = useState<UserAccount | null>(null)
  const query = useQuery({
    queryKey: ['members', state],
    queryFn: ({ signal }) => accessApi.members(state, signal),
    placeholderData: keepPreviousData,
  })
  const statusMutation = useMutation({
    mutationFn: ({ member, status }: { member: UserAccount; status: 'active' | 'disabled' }) =>
      accessApi.setMemberStatus(member.id, status, crypto.randomUUID()),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['members'] }),
  })
  const deleteMutation = useMutation({
    mutationFn: (member: UserAccount) => accessApi.deleteMember(member.id, crypto.randomUUID()),
    async onSuccess() {
      setDeleting(null)
      await queryClient.invalidateQueries({ queryKey: ['members'] })
    },
  })
  const columns = useMemo<ColumnDef<UserAccount, unknown>[]>(
    () => [
      {
        accessorKey: 'displayName',
        header: '成员',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.displayName}</strong>
            <small className="table-subline">{row.original.email}</small>
          </div>
        ),
      },
      { accessorKey: 'keyCount', header: 'API 密钥', meta: { align: 'right' } },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
        meta: { align: 'center' },
      },
      {
        accessorKey: 'createdAt',
        header: '创建时间',
        cell: ({ row }) => formatDateTime(row.original.createdAt),
      },
      {
        id: 'actions',
        header: '操作',
        meta: { align: 'center' },
        cell: ({ row }) =>
          row.original.role === 'member' && row.original.status !== 'deleted' ? (
            <div className="row-actions row-actions--center">
              <TableAction
                label="编辑"
                icon={<Pencil size={16} />}
                onClick={() => setEditing(row.original)}
              />
              {row.original.status === 'active' ? (
                <TableAction
                  label="停用"
                  tone="warning"
                  icon={<Power size={16} />}
                  onClick={() =>
                    statusMutation.mutate({ member: row.original, status: 'disabled' })
                  }
                />
              ) : (
                <TableAction
                  label="启用"
                  tone="positive"
                  icon={<Play size={16} />}
                  onClick={() => statusMutation.mutate({ member: row.original, status: 'active' })}
                />
              )}
              <RowActionMenu>
                <RowActionItem
                  icon={<KeyRound size={15} />}
                  onSelect={() => setPasswordMember(row.original)}
                >
                  重置密码
                </RowActionItem>
                <RowActionSeparator />
                <RowActionItem
                  icon={<Trash2 size={15} />}
                  danger
                  onSelect={() => setDeleting(row.original)}
                >
                  删除成员
                </RowActionItem>
              </RowActionMenu>
            </div>
          ) : null,
      },
    ],
    [statusMutation],
  )

  return (
    <Page>
      <PageHeader
        title="成员"
        actions={
          <Button
            icon={<Plus size={16} />}
            data-onboarding="create-member"
            onClick={() => setCreating(true)}
          >
            创建成员
          </Button>
        }
      />
      <PageSection>
        <FormProblem error={statusMutation.error ?? deleteMutation.error} />
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索成员"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'active', label: '可用' },
            { value: 'disabled', label: '已停用' },
          ]}
        />
        <DataTable
          ariaLabel="成员列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(member) => member.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的成员"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
        />
      </PageSection>
      {creating ? <MemberForm member={null} open onOpenChange={setCreating} /> : null}
      {editing ? (
        <MemberForm member={editing} open onOpenChange={(open) => !open && setEditing(null)} />
      ) : null}
      <MemberPasswordDialog
        user={passwordMember}
        onOpenChange={(open) => !open && setPasswordMember(null)}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="删除成员"
        description="删除后成员将无法登录或发起新请求，历史订阅、请求和账本记录继续保留。"
        confirmLabel="确认删除"
        pending={deleteMutation.isPending}
        danger
        onConfirm={() => deleting && deleteMutation.mutate(deleting)}
      />
    </Page>
  )
}
