import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Check, Edit3, KeyRound, Plus, Power, ServerCog } from 'lucide-react'
import { useMemo, useState } from 'react'

import { catalogApi, type Provider, type ProviderPreset } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { ErrorState, LoadingState } from '@/components/ui/state'
import { FormProblem } from '@/features/auth/form-problem'
import { useSession, hasCapability } from '@/app/session'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { CatalogTabs } from './catalog-tabs'
import { ProviderForm } from './provider-form'
import {
  createProviderOperation,
  hasUnknownProviderOutcome,
  type ProviderOperation,
} from './provider-mutation'
import { ProviderOperationRecovery } from './provider-operation-recovery'

type EditingProvider = { kind: 'create' } | { kind: 'existing'; provider: Provider }
type ProviderStatusVariables = {
  providerID: string
  enabled: boolean
  expectedUpdatedAt: string
}
type ProviderStatusOperation = ProviderOperation<ProviderStatusVariables>
type PresetInstallOperation = ProviderOperation<{ presetID: string }>

export function ProvidersPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'providers:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<EditingProvider | null>(null)
  const [uncertainStatusOperation, setUncertainStatusOperation] = useState<
    ProviderStatusOperation | undefined
  >()
  const [uncertainPresetOperation, setUncertainPresetOperation] = useState<
    PresetInstallOperation | undefined
  >()
  const presets = useQuery({
    queryKey: ['provider-presets'],
    queryFn: ({ signal }) => catalogApi.providerPresets(signal),
  })
  const query = useQuery({
    queryKey: ['providers', state],
    queryFn: ({ signal }) => catalogApi.providers(state, signal),
    placeholderData: keepPreviousData,
  })
  const toggle = useMutation({
    mutationFn: ({ variables, idempotencyKey }: ProviderStatusOperation) =>
      catalogApi.setProviderEnabled(
        variables.providerID,
        variables.enabled,
        variables.expectedUpdatedAt,
        idempotencyKey,
      ),
    onSuccess: () => {
      setUncertainStatusOperation(undefined)
      return queryClient.invalidateQueries({ queryKey: ['providers'] })
    },
    onError: (error, operation) => {
      setUncertainStatusOperation(hasUnknownProviderOutcome(error) ? operation : undefined)
      return queryClient.invalidateQueries({ queryKey: ['providers'] })
    },
  })
  const installPreset = useMutation({
    mutationFn: ({ variables, idempotencyKey }: PresetInstallOperation) =>
      catalogApi.installProviderPreset(variables.presetID, idempotencyKey),
    async onSuccess() {
      setUncertainPresetOperation(undefined)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['provider-presets'] }),
        queryClient.invalidateQueries({ queryKey: ['providers'] }),
        queryClient.invalidateQueries({ queryKey: ['models'] }),
      ])
    },
    onError(error, operation) {
      setUncertainPresetOperation(hasUnknownProviderOutcome(error) ? operation : undefined)
      void queryClient.invalidateQueries({ queryKey: ['provider-presets'] })
    },
  })
  const editingProvider = editing?.kind === 'existing' ? editing.provider : undefined

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
      { accessorKey: 'credentialCount', header: 'API Key' },
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
                onClick={() => setEditing({ kind: 'existing', provider: row.original })}
              >
                <Edit3 size={16} />
              </IconButton>
              <IconButton
                label={row.original.status === 'enabled' ? '停用 Provider' : '启用 Provider'}
                disabled={toggle.isPending || Boolean(uncertainStatusOperation)}
                onClick={() =>
                  toggle.mutate(
                    createProviderOperation({
                      providerID: row.original.id,
                      enabled: row.original.status !== 'enabled',
                      expectedUpdatedAt: row.original.updatedAt,
                    }),
                  )
                }
              >
                <Power size={16} />
              </IconButton>
            </div>
          ) : null,
      },
    ],
    [canWrite, toggle, uncertainStatusOperation],
  )

  return (
    <Page>
      <PageHeader
        title="Provider 接入"
        actions={
          canWrite ? (
            <Button icon={<Plus size={16} />} onClick={() => setEditing({ kind: 'create' })}>
              自定义 Provider
            </Button>
          ) : null
        }
      />
      <CatalogTabs />
      <PageSection>
        {presets.isLoading ? <LoadingState label="正在读取 Provider" /> : null}
        {presets.error ? (
          <ErrorState error={presets.error} onRetry={() => void presets.refetch()} />
        ) : null}
        {uncertainPresetOperation ? (
          <ProviderOperationRecovery
            error={installPreset.error}
            pending={installPreset.isPending}
            onRetry={() => installPreset.mutate(uncertainPresetOperation)}
          />
        ) : installPreset.error ? (
          <FormProblem error={installPreset.error} />
        ) : null}
        {presets.data ? (
          <div className="provider-presets" role="list" aria-label="Provider 接入">
            {presets.data.map((preset) => (
              <ProviderPresetCard
                key={preset.id}
                preset={preset}
                canWrite={canWrite}
                installing={installPreset.isPending}
                onInstall={() =>
                  installPreset.mutate(createProviderOperation({ presetID: preset.id }))
                }
              />
            ))}
          </div>
        ) : null}
      </PageSection>
      <PageSection title="已接入 Provider">
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
        {uncertainStatusOperation ? (
          <ProviderOperationRecovery
            error={toggle.error}
            pending={toggle.isPending}
            onRetry={() => toggle.mutate(uncertainStatusOperation)}
          />
        ) : (
          <FormProblem error={toggle.error} />
        )}
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
            canWrite ? (provider) => setEditing({ kind: 'existing', provider }) : undefined
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
                {provider.modelCount} 个模型 · {provider.credentialCount} 个 API Key
              </span>
            </div>
          )}
        />
      </PageSection>

      <ProviderForm
        key={
          editing?.kind === 'existing'
            ? `provider:${editing.provider.id}:${editing.provider.updatedAt}`
            : (editing?.kind ?? 'closed')
        }
        open={editing?.kind === 'create' || editingProvider !== undefined}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        {...(editingProvider ? { provider: editingProvider } : {})}
      />
    </Page>
  )
}

function ProviderPresetCard({
  preset,
  canWrite,
  installing,
  onInstall,
}: {
  preset: ProviderPreset
  canWrite: boolean
  installing: boolean
  onInstall: () => void
}) {
  const installed = preset.state === 'installed'
  const conflict = preset.state === 'conflict'
  const model = preset.models[0]
  return (
    <article className="provider-preset" role="listitem" data-state={preset.state}>
      <div className="provider-preset__heading">
        <div className="provider-preset__identity">
          <span className="provider-preset__icon" aria-hidden="true">
            <ServerCog size={19} />
          </span>
          <div>
            <strong>{preset.name}</strong>
            <span>{model?.alias ?? preset.kind}</span>
          </div>
        </div>
        <Badge tone={conflict ? 'danger' : installed ? 'positive' : 'neutral'}>
          {conflict ? '需要处理' : installed ? '已接入' : '未接入'}
        </Badge>
      </div>
      <div className="capability-strip" aria-label="模型能力">
        {model?.capabilities.map((capability) => (
          <Badge key={capability}>{capabilityLabel[capability] ?? capability}</Badge>
        ))}
      </div>
      <div className="provider-preset__action">
        {!installed && !conflict && canWrite ? (
          <Button size="sm" disabled={installing} icon={<Check size={15} />} onClick={onInstall}>
            接入
          </Button>
        ) : installed ? (
          <Button
            asChild
            size="sm"
            variant={preset.installedCredentials > 0 ? 'secondary' : 'primary'}
            icon={<KeyRound size={15} />}
          >
            <Link to="/credentials">
              {preset.installedCredentials > 0
                ? `查看 API Key (${preset.installedCredentials})`
                : '添加 Provider API Key'}
            </Link>
          </Button>
        ) : null}
      </div>
    </article>
  )
}

const capabilityLabel: Record<string, string> = {
  streaming: '流式',
  tools: '工具',
  reasoning: '推理',
  structured_output: '结构化输出',
}
