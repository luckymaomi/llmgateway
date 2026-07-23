import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState, type FormEvent } from 'react'

import { catalogApi, type ResourcePool } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

export function ResourcePoolForm({
  pool,
  open,
  onOpenChange,
}: {
  pool: ResourcePool | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [providerId, setProviderId] = useState(pool?.providerId ?? '')
  const [slug, setSlug] = useState(pool?.slug ?? '')
  const [name, setName] = useState(pool?.name ?? '')
  const [modelIds, setModelIds] = useState<string[]>(pool?.models.map((model) => model.id) ?? [])
  const providers = useQuery({
    queryKey: ['providers', 'resource-pool-form'],
    queryFn: ({ signal }) => catalogApi.providers(signal),
    enabled: open && pool === null,
  })
  const models = useQuery({
    queryKey: ['models', 'resource-pool-form'],
    queryFn: ({ signal }) => catalogApi.models(signal),
    enabled: open && pool === null,
  })
  const selectableModels = useMemo(
    () => (models.data ?? []).filter((model) => model.providerId === providerId),
    [models.data, providerId],
  )

  const mutation = useMutation({
    mutationFn: () =>
      pool
        ? catalogApi.updateResourcePool(
            pool.id,
            { name: name.trim(), expectedUpdatedAt: pool.updatedAt },
            crypto.randomUUID(),
          )
        : catalogApi.createResourcePool(
            { providerId, slug: slug.trim(), name: name.trim(), modelIds },
            crypto.randomUUID(),
          ),
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['resource-pools'] })
      onOpenChange(false)
    },
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!name.trim() || (!pool && (!providerId || !slug.trim() || modelIds.length === 0))) return
    mutation.mutate()
  }

  const locked = mutation.isPending
  return (
    <DialogFrame
      open={open}
      onOpenChange={(next) => !locked && onOpenChange(next)}
      title={pool ? '编辑资源池' : '创建资源池'}
      width="lg"
      dismissible={!locked}
      footer={
        <>
          <Button
            type="button"
            variant="secondary"
            disabled={locked}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button type="submit" form="resource-pool-form" disabled={locked}>
            {locked ? '保存中' : '保存'}
          </Button>
        </>
      }
    >
      <form id="resource-pool-form" className="form-grid" onSubmit={submit}>
        <Field label="Provider" htmlFor="pool-provider">
          <NativeSelect
            id="pool-provider"
            autoFocus
            value={providerId}
            disabled={locked || pool !== null}
            onChange={(event) => {
              setProviderId(event.target.value)
              setModelIds([])
            }}
          >
            <option value="">请选择</option>
            {(providers.data ?? []).map((provider) => (
              <option key={provider.id} value={provider.id}>
                {provider.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="资源池名称" htmlFor="pool-name">
          <Input
            id="pool-name"
            value={name}
            readOnly={locked}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="标识" htmlFor="pool-slug" hint="创建后保持稳定">
          <Input
            id="pool-slug"
            value={slug}
            readOnly={locked || pool !== null}
            onChange={(event) => setSlug(event.target.value.toLowerCase())}
          />
        </Field>
        {pool ? (
          <Field label="模型" htmlFor="pool-model-summary">
            <Input
              id="pool-model-summary"
              value={pool.models.map((model) => model.publicName).join('、')}
              readOnly
            />
          </Field>
        ) : (
          <fieldset className="choice-field field--full">
            <legend>池内模型</legend>
            <div className="choice-grid">
              {selectableModels.map((model) => (
                <label key={model.id}>
                  <input
                    type="checkbox"
                    checked={modelIds.includes(model.id)}
                    disabled={locked}
                    onChange={(event) =>
                      setModelIds((current) =>
                        event.target.checked
                          ? [...current, model.id]
                          : current.filter((id) => id !== model.id),
                      )
                    }
                  />
                  <span>{model.publicName}</span>
                </label>
              ))}
            </div>
            {providerId === '' ? (
              <p className="choice-field__empty">选择 Provider 后显示可用模型</p>
            ) : providers.isLoading || models.isLoading ? (
              <p className="choice-field__empty">正在读取可用模型</p>
            ) : selectableModels.length === 0 ? (
              <p className="choice-field__empty">该 Provider 当前没有可用模型</p>
            ) : null}
          </fieldset>
        )}
        <FormProblem error={mutation.error ?? providers.error ?? models.error} />
      </form>
    </DialogFrame>
  )
}
