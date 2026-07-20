import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Edit3, Plus, Power } from 'lucide-react'
import { useMemo, useState } from 'react'

import { catalogApi, type Provider } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { FormProblem } from '@/features/auth/form-problem'
import { useSession, hasCapability } from '@/app/session'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { CatalogTabs } from './catalog-tabs'
import { ProviderForm } from './provider-form'

type EditingProvider = { kind: 'create' } | { kind: 'existing'; id: string }

export function ProvidersPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'providers:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<EditingProvider | null>(null)
  const query = useQuery({
    queryKey: ['providers', state],
    queryFn: ({ signal }) => catalogApi.providers(state, signal),
    placeholderData: keepPreviousData,
  })
  const toggle = useMutation({
    mutationFn: (provider: Provider) =>
      catalogApi.setProviderEnabled(provider.id, provider.status !== 'enabled', provider.updatedAt),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['providers'] }),
    onError: () => queryClient.invalidateQueries({ queryKey: ['providers'] }),
  })
  const editingProvider =
    editing?.kind === 'existing'
      ? query.data?.items.find((provider) => provider.id === editing.id)
      : undefined

  const columns = useMemo<ColumnDef<Provider, unknown>[]>(
    () => [
      {
        accessorKey: 'name',
        header: 'Provider',
        cell: ({ row }) => (
          <span>
            <strong>{row.original.name}</strong>
            <small className="table-subline">{row.original.slug}</small>
          </span>
        ),
      },
      { accessorKey: 'kind', header: '类型' },
      { accessorKey: 'modelCount', header: '模型' },
      { accessorKey: 'credentialCount', header: '凭据' },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        accessorKey: 'verifiedAt',
        header: '最近核验',
        cell: ({ row }) => formatDateTime(row.original.verifiedAt),
      },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) =>
          canWrite ? (
            <div className="row-actions" onClick={(event) => event.stopPropagation()}>
              <IconButton
                label="编辑 Provider"
                onClick={() => setEditing({ kind: 'existing', id: row.original.id })}
              >
                <Edit3 size={16} />
              </IconButton>
              <IconButton
                label={row.original.status === 'enabled' ? '停用 Provider' : '启用 Provider'}
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
        title="Provider 与模型"
        description="上游端点、模型能力与可发布配置"
        actions={
          canWrite ? (
            <Button icon={<Plus size={16} />} onClick={() => setEditing({ kind: 'create' })}>
              添加 Provider
            </Button>
          ) : null
        }
      />
      <CatalogTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索 Provider"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'enabled', label: '已启用' },
            { value: 'disabled', label: '已停用' },
          ]}
        />
        <FormProblem error={toggle.error} />
        <DataTable
          ariaLabel="Provider 列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(provider) => provider.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的 Provider"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          onRowClick={
            canWrite ? (provider) => setEditing({ kind: 'existing', id: provider.id }) : undefined
          }
          renderMobile={(provider) => (
            <div className="mobile-summary">
              <div>
                <strong>{provider.name}</strong>
                <StatusBadge status={provider.status} />
              </div>
              <span>
                {provider.slug} · {provider.kind}
              </span>
              <span>
                {provider.modelCount} 个模型 · {provider.credentialCount} 个凭据
              </span>
            </div>
          )}
        />
      </PageSection>

      <ProviderForm
        open={editing?.kind === 'create' || editingProvider !== undefined}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        {...(editingProvider ? { provider: editingProvider } : {})}
      />
    </Page>
  )
}
