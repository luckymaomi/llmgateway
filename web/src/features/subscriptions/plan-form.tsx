import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState, type FormEvent } from 'react'

import {
  catalogApi,
  subscriptionsApi,
  type PlanInput,
  type PlanKind,
  type ServicePlan,
} from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect, Textarea } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

interface EditableRoute {
  modelId: string
  resourcePoolId: string
}

export function PlanForm({
  plan,
  open,
  onOpenChange,
}: {
  plan: ServicePlan | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const version = plan?.currentVersion
  const [name, setName] = useState(plan?.name ?? '')
  const [description, setDescription] = useState(plan?.description ?? '')
  const [kind, setKind] = useState<PlanKind>(plan?.kind ?? 'token')
  const [tokenQuota, setTokenQuota] = useState(version?.tokenQuota ?? 1_000_000)
  const [validityDays, setValidityDays] = useState(version?.validityDays ?? 30)
  const [rpmLimit, setRpmLimit] = useState(version?.rpmLimit ?? 0)
  const [tpmLimit, setTpmLimit] = useState(version?.tpmLimit ?? 0)
  const [concurrencyLimit, setConcurrencyLimit] = useState(version?.concurrencyLimit ?? 1)
  const [routes, setRoutes] = useState<EditableRoute[]>(
    version?.routes.map(({ modelId, resourcePoolId }) => ({ modelId, resourcePoolId })) ?? [],
  )
  const pools = useQuery({
    queryKey: ['resource-pools', 'plan-form'],
    queryFn: ({ signal }) => catalogApi.resourcePools(false, signal),
    enabled: open,
  })
  const models = useMemo(() => {
    const byId = new Map<string, { id: string; name: string; publicName: string }>()
    for (const pool of pools.data ?? []) {
      for (const model of pool.models) {
        byId.set(model.id, {
          id: model.id,
          name: model.displayName,
          publicName: model.publicName,
        })
      }
    }
    return Array.from(byId.values()).sort((left, right) => left.name.localeCompare(right.name))
  }, [pools.data])

  const mutation = useMutation({
    mutationFn: () => {
      const input: PlanInput = {
        name: name.trim(),
        description: description.trim(),
        kind,
        tokenQuota,
        validityDays,
        concurrencyLimit,
        routes,
        ...(rpmLimit > 0 ? { rpmLimit } : {}),
        ...(tpmLimit > 0 ? { tpmLimit } : {}),
      }
      return subscriptionsApi.publishPlan(input, crypto.randomUUID(), plan?.id)
    },
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['plans'] })
      onOpenChange(false)
    },
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (
      !name.trim() ||
      tokenQuota < 1 ||
      validityDays < 1 ||
      concurrencyLimit < 1 ||
      routes.length === 0 ||
      routes.some((route) => !route.resourcePoolId)
    ) {
      return
    }
    mutation.mutate()
  }

  const locked = mutation.isPending
  return (
    <DialogFrame
      open={open}
      onOpenChange={(next) => !locked && onOpenChange(next)}
      title={plan ? '发布套餐新版本' : '创建并发布套餐'}
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
          <Button type="submit" form="plan-form" disabled={locked}>
            {locked ? '发布中' : '发布版本'}
          </Button>
        </>
      }
    >
      <form id="plan-form" className="form-grid" onSubmit={submit}>
        <Field label="套餐名称" htmlFor="plan-name">
          <Input
            id="plan-name"
            autoFocus
            required
            value={name}
            readOnly={locked}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="套餐用途" htmlFor="plan-kind">
          <NativeSelect
            id="plan-kind"
            required
            value={kind}
            disabled={locked}
            onChange={(event) => setKind(event.target.value as PlanKind)}
          >
            <option value="token">通用 Token 套餐</option>
            <option value="coding">编程套餐</option>
          </NativeSelect>
        </Field>
        <Field
          label="每份订阅总额度（Token）"
          htmlFor="plan-tokens"
          hint="输入和输出 Token 都会从这份额度中扣除"
        >
          <Input
            id="plan-tokens"
            type="number"
            required
            min={1}
            value={tokenQuota}
            readOnly={locked}
            onChange={(event) => setTokenQuota(Number(event.target.value))}
          />
        </Field>
        <Field label="订阅有效天数" htmlFor="plan-validity">
          <Input
            id="plan-validity"
            type="number"
            required
            min={1}
            max={3650}
            value={validityDays}
            readOnly={locked}
            onChange={(event) => setValidityDays(Number(event.target.value))}
          />
        </Field>
        <Field label="同时请求上限" htmlFor="plan-concurrency">
          <Input
            id="plan-concurrency"
            type="number"
            required
            min={1}
            value={concurrencyLimit}
            readOnly={locked}
            onChange={(event) => setConcurrencyLimit(Number(event.target.value))}
          />
        </Field>
        <Field label="每分钟请求上限（RPM）" htmlFor="plan-rpm" hint="0 表示不额外限制">
          <Input
            id="plan-rpm"
            type="number"
            min={0}
            value={rpmLimit}
            readOnly={locked}
            onChange={(event) => setRpmLimit(Number(event.target.value))}
          />
        </Field>
        <Field label="每分钟 Token 上限（TPM）" htmlFor="plan-tpm" hint="0 表示不额外限制">
          <Input
            id="plan-tpm"
            type="number"
            min={0}
            value={tpmLimit}
            readOnly={locked}
            onChange={(event) => setTpmLimit(Number(event.target.value))}
          />
        </Field>
        <Field label="说明" htmlFor="plan-description" className="field--full">
          <Textarea
            id="plan-description"
            rows={3}
            value={description}
            readOnly={locked}
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>
        <fieldset className="choice-field field--full">
          <legend>套餐包含的模型</legend>
          <p className="choice-field__hint">勾选成员可用的模型，并指定请求只能使用哪个资源池</p>
          <div className="binding-grid">
            {models.map((model) => {
              const route = routes.find((item) => item.modelId === model.id)
              const modelPools = (pools.data ?? []).filter((pool) =>
                pool.models.some((item) => item.id === model.id),
              )
              return (
                <div className="binding-row" key={model.id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={route !== undefined}
                      disabled={locked}
                      onChange={(event) =>
                        setRoutes((current) =>
                          event.target.checked
                            ? [
                                ...current,
                                { modelId: model.id, resourcePoolId: modelPools[0]?.id ?? '' },
                              ]
                            : current.filter((item) => item.modelId !== model.id),
                        )
                      }
                    />
                    <span>
                      {model.name}
                      <small className="table-subline">{model.publicName}</small>
                    </span>
                  </label>
                  <NativeSelect
                    aria-label={`${model.name} 资源池`}
                    value={route?.resourcePoolId ?? ''}
                    disabled={!route || locked}
                    onChange={(event) =>
                      setRoutes((current) =>
                        current.map((item) =>
                          item.modelId === model.id
                            ? { ...item, resourcePoolId: event.target.value }
                            : item,
                        ),
                      )
                    }
                  >
                    {modelPools.map((pool) => (
                      <option key={pool.id} value={pool.id}>
                        {pool.name}
                      </option>
                    ))}
                  </NativeSelect>
                </div>
              )
            })}
          </div>
          {pools.isLoading ? (
            <p className="choice-field__empty">正在读取资源池模型</p>
          ) : models.length === 0 ? (
            <p className="choice-field__empty">当前没有可发布到套餐的资源池模型</p>
          ) : null}
          {models.length > 0 && routes.length === 0 ? (
            <span className="field__error">至少选择一个模型并指定资源池</span>
          ) : null}
        </fieldset>
        <FormProblem error={mutation.error ?? pools.error} />
      </form>
    </DialogFrame>
  )
}
