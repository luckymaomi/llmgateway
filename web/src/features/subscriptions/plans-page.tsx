import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Archive, Pencil, Play, Plus, Power } from 'lucide-react'
import { useMemo, useState } from 'react'

import { subscriptionsApi, type PlanStatus, type ServicePlan } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { RowActionItem, RowActionMenu, TableAction } from '@/components/data-table/row-actions'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { FormProblem } from '@/features/auth/form-problem'
import { formatNumber } from '@/lib/format'

import { PlanForm } from './plan-form'

export function PlansPage() {
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<ServicePlan | null>(null)
  const [statusTarget, setStatusTarget] = useState<{
    plan: ServicePlan
    status: PlanStatus
  } | null>(null)
  const query = useQuery({
    queryKey: ['plans'],
    queryFn: ({ signal }) => subscriptionsApi.plans(true, signal),
  })
  const statusMutation = useMutation({
    mutationFn: ({ plan, status }: NonNullable<typeof statusTarget>) =>
      subscriptionsApi.setPlanStatus(plan.id, status, crypto.randomUUID()),
    async onSuccess() {
      setStatusTarget(null)
      await queryClient.invalidateQueries({ queryKey: ['plans'] })
    },
  })
  const columns = useMemo<ColumnDef<ServicePlan, unknown>[]>(
    () => [
      {
        accessorKey: 'name',
        header: '套餐',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.name}</strong>
            <small className="table-subline">
              <code>{row.original.slug}</code>
            </small>
          </div>
        ),
      },
      {
        accessorKey: 'kind',
        header: '类型',
        cell: ({ row }) => (row.original.kind === 'coding' ? '编程套餐' : '通用 Token 套餐'),
      },
      {
        id: 'version',
        header: '版本',
        cell: ({ row }) =>
          row.original.currentVersion ? `v${row.original.currentVersion.version}` : '—',
        meta: { align: 'right' },
      },
      {
        id: 'quota',
        header: '总额度（Token）',
        cell: ({ row }) => formatNumber(row.original.currentVersion?.tokenQuota ?? 0),
        meta: { align: 'right' },
      },
      {
        id: 'routes',
        header: '模型',
        cell: ({ row }) =>
          row.original.currentVersion?.routes.map((route) => route.modelName).join('、') ?? '—',
      },
      { accessorKey: 'activeSubscriptionCount', header: '活动订阅', meta: { align: 'right' } },
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
          row.original.status !== 'archived' ? (
            <div className="row-actions row-actions--center">
              <TableAction
                label="新版本"
                icon={<Pencil size={16} />}
                onClick={() => setEditing(row.original)}
              />
              {row.original.status === 'active' ? (
                <TableAction
                  label="停用"
                  tone="warning"
                  icon={<Power size={16} />}
                  onClick={() => setStatusTarget({ plan: row.original, status: 'disabled' })}
                />
              ) : (
                <>
                  <TableAction
                    label="启用"
                    tone="positive"
                    icon={<Play size={16} />}
                    onClick={() => setStatusTarget({ plan: row.original, status: 'active' })}
                  />
                  <RowActionMenu>
                    <RowActionItem
                      icon={<Archive size={15} />}
                      danger
                      onSelect={() => setStatusTarget({ plan: row.original, status: 'archived' })}
                    >
                      归档套餐
                    </RowActionItem>
                  </RowActionMenu>
                </>
              )}
            </div>
          ) : null,
      },
    ],
    [],
  )

  return (
    <Page>
      <PageHeader
        title="套餐"
        actions={
          <Button
            icon={<Plus size={16} />}
            data-onboarding="create-plan"
            onClick={() => setCreating(true)}
          >
            创建套餐
          </Button>
        }
      />
      <PageSection>
        <FormProblem error={statusMutation.error} />
        <DataTable
          ariaLabel="套餐列表"
          data={query.data ?? []}
          columns={columns}
          getRowId={(plan) => plan.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="还没有已发布套餐"
          page={1}
          pageSize={Math.max(query.data?.length ?? 0, 1)}
          total={query.data?.length ?? 0}
          onPageChange={() => undefined}
        />
      </PageSection>
      {creating ? <PlanForm plan={null} open onOpenChange={setCreating} /> : null}
      {editing ? (
        <PlanForm plan={editing} open onOpenChange={(open) => !open && setEditing(null)} />
      ) : null}
      <ConfirmDialog
        open={statusTarget !== null}
        onOpenChange={(open) => !open && setStatusTarget(null)}
        title={statusTarget?.status === 'archived' ? '归档套餐' : '更改套餐状态'}
        description="状态变更只影响新订阅，既有订阅继续引用原不可变版本。"
        confirmLabel="确认"
        pending={statusMutation.isPending}
        danger={statusTarget?.status === 'archived'}
        onConfirm={() => statusTarget && statusMutation.mutate(statusTarget)}
      />
    </Page>
  )
}
