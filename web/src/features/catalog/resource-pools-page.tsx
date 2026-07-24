import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Archive, Pencil, Play, Plus, Power } from 'lucide-react'
import { useMemo, useState } from 'react'

import { catalogApi, type ResourcePool, type ResourcePoolStatus } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { RowActionItem, RowActionMenu, TableAction } from '@/components/data-table/row-actions'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { FormProblem } from '@/features/auth/form-problem'

import { ResourcePoolForm } from './resource-pool-form'

export function ResourcePoolsPage() {
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<ResourcePool | null>(null)
  const [statusTarget, setStatusTarget] = useState<{
    pool: ResourcePool
    status: ResourcePoolStatus
  } | null>(null)
  const query = useQuery({
    queryKey: ['resource-pools'],
    queryFn: ({ signal }) => catalogApi.resourcePools(true, signal),
  })
  const statusMutation = useMutation({
    mutationFn: ({ pool, status }: NonNullable<typeof statusTarget>) =>
      catalogApi.setResourcePoolStatus(pool.id, status, pool.updatedAt, crypto.randomUUID()),
    async onSuccess() {
      setStatusTarget(null)
      await queryClient.invalidateQueries({ queryKey: ['resource-pools'] })
      await queryClient.invalidateQueries({ queryKey: ['providers'] })
    },
  })
  const columns = useMemo<ColumnDef<ResourcePool, unknown>[]>(
    () => [
      {
        accessorKey: 'name',
        header: '资源池',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.name}</strong>
            <small className="table-subline">
              <code>{row.original.slug}</code>
            </small>
          </div>
        ),
      },
      { accessorKey: 'providerName', header: '上游平台' },
      {
        id: 'models',
        header: '模型',
        cell: ({ row }) => row.original.models.map((model) => model.displayName).join('、'),
      },
      { accessorKey: 'credentialCount', header: '上游 Key 总数', meta: { align: 'right' } },
      {
        accessorKey: 'activeCredentialCount',
        header: '可用 Key',
        meta: { align: 'right' },
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
        cell: ({ row }) => (
          <div className="row-actions row-actions--center">
            {row.original.status !== 'retired' ? (
              <TableAction
                label="编辑"
                icon={<Pencil size={16} />}
                onClick={() => setEditing(row.original)}
              />
            ) : null}
            {row.original.status === 'active' ? (
              <TableAction
                label="停用"
                tone="warning"
                icon={<Power size={16} />}
                onClick={() => setStatusTarget({ pool: row.original, status: 'disabled' })}
              />
            ) : row.original.status === 'disabled' ? (
              <>
                <TableAction
                  label="启用"
                  tone="positive"
                  icon={<Play size={16} />}
                  onClick={() => setStatusTarget({ pool: row.original, status: 'active' })}
                />
                <RowActionMenu>
                  <RowActionItem
                    icon={<Archive size={15} />}
                    danger
                    onSelect={() => setStatusTarget({ pool: row.original, status: 'retired' })}
                  >
                    退役资源池
                  </RowActionItem>
                </RowActionMenu>
              </>
            ) : null}
          </div>
        ),
      },
    ],
    [],
  )

  return (
    <Page>
      <PageHeader
        title="资源池"
        actions={
          <Button
            icon={<Plus size={16} />}
            data-onboarding="create-resource-pool"
            onClick={() => setCreating(true)}
          >
            创建资源池
          </Button>
        }
      />
      <PageSection>
        <FormProblem error={statusMutation.error} />
        <DataTable
          ariaLabel="资源池列表"
          data={query.data ?? []}
          columns={columns}
          getRowId={(pool) => pool.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="还没有资源池"
          page={1}
          pageSize={Math.max(query.data?.length ?? 0, 1)}
          total={query.data?.length ?? 0}
          onPageChange={() => undefined}
        />
      </PageSection>
      {creating ? <ResourcePoolForm pool={null} open onOpenChange={setCreating} /> : null}
      {editing ? (
        <ResourcePoolForm pool={editing} open onOpenChange={(open) => !open && setEditing(null)} />
      ) : null}
      <ConfirmDialog
        open={statusTarget !== null}
        onOpenChange={(open) => !open && setStatusTarget(null)}
        title={statusTarget?.status === 'retired' ? '退役资源池' : '更改资源池状态'}
        description={
          statusTarget?.status === 'retired'
            ? '退役后不再接受新请求，历史请求和账本引用仍会保留。'
            : '状态提交后会直接影响新请求的资格判断。'
        }
        confirmLabel="确认"
        pending={statusMutation.isPending}
        danger={statusTarget?.status === 'retired'}
        onConfirm={() => statusTarget && statusMutation.mutate(statusTarget)}
      />
    </Page>
  )
}
