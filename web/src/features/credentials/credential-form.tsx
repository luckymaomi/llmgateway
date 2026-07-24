import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState, type FormEvent } from 'react'

import { catalogApi, type Credential, type CredentialModelBinding } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

type EditableBinding = Omit<CredentialModelBinding, 'modelName'>

export function CredentialForm({
  credential,
  open,
  onOpenChange,
}: {
  credential: Credential
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [resourcePoolId] = useState(credential.resourcePoolId)
  const [name, setName] = useState(credential.name)
  const [secret, setSecret] = useState('')
  const [rpmLimit, setRpmLimit] = useState(credential.rpmLimit ?? 0)
  const [tpmLimit, setTpmLimit] = useState(credential.tpmLimit ?? 0)
  const [concurrencyLimit, setConcurrencyLimit] = useState(credential.concurrencyLimit ?? 0)
  const [bindings, setBindings] = useState<EditableBinding[]>(
    credential.modelBindings.map(({ modelId, priority, weight }) => ({
      modelId,
      priority,
      weight,
    })) ?? [],
  )
  const pools = useQuery({
    queryKey: ['resource-pools', 'credential-form'],
    queryFn: ({ signal }) => catalogApi.resourcePools(false, signal),
    enabled: open,
  })
  const selectedPool = useMemo(
    () => pools.data?.find((pool) => pool.id === resourcePoolId),
    [pools.data, resourcePoolId],
  )

  const mutation = useMutation({
    mutationFn: () => {
      const limits = {
        ...(rpmLimit > 0 ? { rpmLimit } : {}),
        ...(tpmLimit > 0 ? { tpmLimit } : {}),
        ...(concurrencyLimit > 0 ? { concurrencyLimit } : {}),
      }
      return catalogApi.updateCredential(
        credential.id,
        {
          name: name.trim(),
          secret,
          modelBindings: bindings,
          expectedUpdatedAt: credential.updatedAt,
          ...limits,
        },
        crypto.randomUUID(),
      )
    },
    async onSuccess() {
      setSecret('')
      await queryClient.invalidateQueries({ queryKey: ['credentials'] })
      await queryClient.invalidateQueries({ queryKey: ['resource-pools'] })
      onOpenChange(false)
    },
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!resourcePoolId || !name.trim() || bindings.length === 0) return
    mutation.mutate()
  }

  const locked = mutation.isPending
  return (
    <DialogFrame
      open={open}
      onOpenChange={(next) => !locked && onOpenChange(next)}
      title="编辑上游 API Key"
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
          <Button type="submit" form="credential-form" disabled={locked}>
            {locked ? '保存中' : '保存'}
          </Button>
        </>
      }
    >
      <form id="credential-form" className="form-grid" onSubmit={submit}>
        <Field label="资源池" htmlFor="credential-pool">
          <NativeSelect id="credential-pool" required value={resourcePoolId} disabled>
            <option value="">选择资源池</option>
            {(pools.data ?? []).map((pool) => (
              <option key={pool.id} value={pool.id}>
                {pool.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Key 名称" htmlFor="credential-name" hint="用于区分不同的上游 Key">
          <Input
            id="credential-name"
            autoFocus
            required
            value={name}
            readOnly={locked}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field
          label="上游 API Key"
          htmlFor="credential-secret"
          className="field--full"
          hint="不填写则保留当前 Key；填写后立即替换"
        >
          <Input
            id="credential-secret"
            type="password"
            autoComplete="new-password"
            value={secret}
            readOnly={locked}
            onChange={(event) => setSecret(event.target.value)}
          />
        </Field>
        <fieldset className="choice-field field--full">
          <legend>可用模型与调度</legend>
          <p className="choice-field__hint">
            勾选这个 Key 可以调用的模型；数字越小越优先，优先级相同时按权重分配
          </p>
          <div className="binding-grid">
            {(selectedPool?.models ?? []).map((model) => {
              const binding = bindings.find((item) => item.modelId === model.id)
              return (
                <div className="binding-row binding-row--weighted" key={model.id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={binding !== undefined}
                      disabled={locked}
                      onChange={(event) =>
                        setBindings((current) =>
                          event.target.checked
                            ? [...current, { modelId: model.id, priority: 0, weight: 1 }]
                            : current.filter((item) => item.modelId !== model.id),
                        )
                      }
                    />
                    <span>
                      {model.displayName}
                      <small className="table-subline">{model.publicName}</small>
                    </span>
                  </label>
                  <label className="binding-row__value">
                    <span>优先级</span>
                    <Input
                      aria-label={`${model.publicName} 优先级`}
                      type="number"
                      min={0}
                      value={binding?.priority ?? 0}
                      disabled={!binding || locked}
                      onChange={(event) =>
                        updateBinding(model.id, 'priority', Number(event.target.value))
                      }
                    />
                  </label>
                  <label className="binding-row__value">
                    <span>权重</span>
                    <Input
                      aria-label={`${model.publicName} 权重`}
                      type="number"
                      min={1}
                      value={binding?.weight ?? 1}
                      disabled={!binding || locked}
                      onChange={(event) =>
                        updateBinding(model.id, 'weight', Number(event.target.value))
                      }
                    />
                  </label>
                </div>
              )
            })}
          </div>
          {resourcePoolId === '' ? (
            <p className="choice-field__empty">选择资源池后显示可用模型</p>
          ) : pools.isLoading ? (
            <p className="choice-field__empty">正在读取资源池模型</p>
          ) : (selectedPool?.models.length ?? 0) === 0 ? (
            <p className="choice-field__empty">该资源池当前没有可用模型</p>
          ) : null}
          {resourcePoolId && (selectedPool?.models.length ?? 0) > 0 && bindings.length === 0 ? (
            <span className="field__error">至少选择一个可用模型</span>
          ) : null}
        </fieldset>
        <Field
          label="每分钟请求上限（RPM）"
          htmlFor="credential-rpm"
          hint="0 表示跟随上游本身的限制"
        >
          <Input
            id="credential-rpm"
            type="number"
            min={0}
            value={rpmLimit}
            readOnly={locked}
            onChange={(event) => setRpmLimit(Number(event.target.value))}
          />
        </Field>
        <Field
          label="每分钟 Token 上限（TPM）"
          htmlFor="credential-tpm"
          hint="0 表示跟随上游本身的限制"
        >
          <Input
            id="credential-tpm"
            type="number"
            min={0}
            value={tpmLimit}
            readOnly={locked}
            onChange={(event) => setTpmLimit(Number(event.target.value))}
          />
        </Field>
        <Field
          label="同时请求上限"
          htmlFor="credential-concurrency"
          hint="0 表示跟随上游本身的限制"
        >
          <Input
            id="credential-concurrency"
            type="number"
            min={0}
            value={concurrencyLimit}
            readOnly={locked}
            onChange={(event) => setConcurrencyLimit(Number(event.target.value))}
          />
        </Field>
        <FormProblem error={mutation.error ?? pools.error} />
      </form>
    </DialogFrame>
  )

  function updateBinding(modelId: string, field: 'priority' | 'weight', value: number) {
    setBindings((current) =>
      current.map((binding) =>
        binding.modelId === modelId
          ? { ...binding, [field]: Math.max(field === 'weight' ? 1 : 0, value) }
          : binding,
      ),
    )
  }
}
