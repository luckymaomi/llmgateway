import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, Pencil, Play, Plus, Power } from 'lucide-react'
import { useMemo, useState } from 'react'

import { subscriptionsApi, type Subscription, type SubscriptionStatus } from '@/api'
import { useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { FormProblem } from '@/features/auth/form-problem'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatNumber } from '@/lib/format'

import { SubscriptionForm } from './subscription-form'

export function SubscriptionsPage() {
  const session = useSession()
  const administrator = session.role === 'administrator'
  const queryClient = useQueryClient()
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Subscription | null>(null)
  const [statusTarget, setStatusTarget] = useState<{
    subscription: Subscription
    status: SubscriptionStatus
  } | null>(null)
  const query = useQuery({
    queryKey: ['subscriptions', state],
    queryFn: ({ signal }) => subscriptionsApi.subscriptions(state, signal),
    placeholderData: keepPreviousData,
  })
  const statusMutation = useMutation({
    mutationFn: ({ subscription, status }: NonNullable<typeof statusTarget>) =>
      subscriptionsApi.setSubscriptionStatus(
        subscription.id,
        status,
        subscription.updatedAt,
        crypto.randomUUID(),
      ),
    async onSuccess() {
      setStatusTarget(null)
      await queryClient.invalidateQueries({ queryKey: ['subscriptions'] })
    },
  })
  const columns = useMemo<ColumnDef<Subscription, unknown>[]>(
    () => [
      ...(administrator
        ? [
            {
              accessorKey: 'memberName',
              header: '成员',
              cell: ({ row }) => (
                <div>
                  <strong>{row.original.memberName}</strong>
                  <small className="table-subline">{row.original.memberEmail}</small>
                </div>
              ),
            } as ColumnDef<Subscription, unknown>,
          ]
        : []),
      {
        accessorKey: 'servicePlanName',
        header: '套餐',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.servicePlanName}</strong>
            <small className="table-subline">
              v{row.original.planVersion} ·{' '}
              {row.original.planKind === 'coding' ? 'Coding Plan' : 'Token Plan'}
            </small>
          </div>
        ),
      },
      {
        accessorKey: 'grantedTokens',
        header: '发放额度',
        cell: ({ row }) => formatNumber(row.original.grantedTokens),
        meta: { align: 'right' },
      },
      {
        accessorKey: 'balanceTokens',
        header: '当前余额',
        cell: ({ row }) => formatNumber(row.original.balanceTokens),
        meta: { align: 'right' },
      },
      {
        accessorKey: 'startsAt',
        header: '开始',
        cell: ({ row }) => formatDateTime(row.original.startsAt),
      },
      {
        accessorKey: 'expiresAt',
        header: '到期',
        cell: ({ row }) => formatDateTime(row.original.expiresAt),
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
        meta: { align: 'center' },
      },
      ...(administrator
        ? [
            {
              id: 'actions',
              header: '操作',
              meta: { align: 'center' },
              cell: ({ row }) =>
                !['canceled', 'expired'].includes(row.original.status) ? (
                  <div className="row-actions row-actions--center">
                    <Button
                      size="sm"
                      variant="quiet"
                      icon={<Pencil size={14} />}
                      onClick={() => setEditing(row.original)}
                    >
                      调整
                    </Button>
                    {row.original.status === 'suspended' ? (
                      <Button
                        size="sm"
                        variant="quiet"
                        icon={<Play size={14} />}
                        onClick={() =>
                          setStatusTarget({ subscription: row.original, status: 'active' })
                        }
                      >
                        恢复
                      </Button>
                    ) : (
                      <Button
                        size="sm"
                        variant="quiet"
                        icon={<Power size={14} />}
                        onClick={() =>
                          setStatusTarget({ subscription: row.original, status: 'suspended' })
                        }
                      >
                        暂停
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="quiet"
                      icon={<Ban size={14} />}
                      onClick={() =>
                        setStatusTarget({ subscription: row.original, status: 'canceled' })
                      }
                    >
                      取消
                    </Button>
                  </div>
                ) : null,
            } as ColumnDef<Subscription, unknown>,
          ]
        : []),
    ],
    [administrator],
  )

  return (
    <Page>
      <PageHeader
        title={administrator ? '订阅' : '我的订阅'}
        actions={
          administrator ? (
            <Button
              icon={<Plus size={16} />}
              data-onboarding="create-subscription"
              onClick={() => setCreating(true)}
            >
              分配订阅
            </Button>
          ) : undefined
        }
      />
      <PageSection>
        <FormProblem error={statusMutation.error} />
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索成员或套餐"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'scheduled', label: '待生效' },
            { value: 'active', label: '可用' },
            { value: 'suspended', label: '已暂停' },
            { value: 'canceled', label: '已取消' },
            { value: 'expired', label: '已过期' },
          ]}
        />
        <DataTable
          ariaLabel="订阅列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(subscription) => subscription.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel={administrator ? '还没有订阅' : '当前没有订阅'}
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
        />
      </PageSection>
      {administrator && creating ? (
        <SubscriptionForm subscription={null} open onOpenChange={setCreating} />
      ) : null}
      {editing ? (
        <SubscriptionForm
          subscription={editing}
          open
          onOpenChange={(open) => !open && setEditing(null)}
        />
      ) : null}
      <ConfirmDialog
        open={statusTarget !== null}
        onOpenChange={(open) => !open && setStatusTarget(null)}
        title={statusTarget?.status === 'canceled' ? '取消订阅' : '更改订阅状态'}
        description="状态提交后会直接影响该成员的新请求资格，已完成请求和账本保持不变。"
        confirmLabel="确认"
        pending={statusMutation.isPending}
        danger={statusTarget?.status === 'canceled'}
        onConfirm={() => statusTarget && statusMutation.mutate(statusTarget)}
      />
    </Page>
  )
}
