import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useMemo, useState, type FormEvent } from 'react'

import { accessApi, subscriptionsApi, type CreatedGatewayKey } from '@/api'
import { useSession } from '@/app/session'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

export function KeyForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const session = useSession()
  const [ownerId, setOwnerId] = useState('')
  const [name, setName] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  const [modelIds, setModelIds] = useState<string[]>([])
  const [created, setCreated] = useState<CreatedGatewayKey>()
  const [copied, setCopied] = useState(false)
  const [operationKey, setOperationKey] = useState('')
  const members = useQuery({
    queryKey: ['members', 'key-form'],
    queryFn: ({ signal }) =>
      accessApi.members({ page: 1, pageSize: 100, status: 'active' }, signal),
    enabled: open && session.role === 'administrator',
  })
  const effectiveOwnerId = session.role === 'member' ? session.userId : ownerId
  const subscriptions = useQuery({
    queryKey: ['subscriptions', 'key-form', effectiveOwnerId],
    queryFn: ({ signal }) =>
      subscriptionsApi.subscriptions(
        { page: 1, pageSize: 100, userId: effectiveOwnerId, status: 'active' },
        signal,
      ),
    enabled: open && Boolean(effectiveOwnerId),
  })
  const authorizedModels = useMemo(() => {
    const allowed = new Map<string, string>()
    for (const subscription of subscriptions.data?.items ?? []) {
      subscription.routes.forEach((route) => allowed.set(route.modelId, route.modelName))
    }
    return Array.from(allowed, ([id, publicName]) => ({ id, publicName }))
  }, [subscriptions.data?.items])
  const mutation = useMutation({
    mutationFn: () =>
      accessApi.createKey(
        {
          ownerId: effectiveOwnerId,
          name: name.trim(),
          authorizedModelIds: modelIds,
          ...(expiresAt ? { expiresAt: new Date(expiresAt).toISOString() } : {}),
        },
        operationKey || crypto.randomUUID(),
      ),
    async onSuccess(result) {
      setCreated(result)
      await queryClient.invalidateQueries({ queryKey: ['gateway-keys'] })
    },
  })

  function close() {
    if (mutation.isPending) return
    setOwnerId('')
    setName('')
    setExpiresAt('')
    setModelIds([])
    setCreated(undefined)
    setCopied(false)
    setOperationKey('')
    mutation.reset()
    onOpenChange(false)
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!effectiveOwnerId || !name.trim() || modelIds.length === 0) return
    if (!operationKey) setOperationKey(crypto.randomUUID())
    mutation.mutate()
  }

  const gatewayBaseURL = `${window.location.origin}/v1`
  return (
    <DialogFrame
      open={open}
      onOpenChange={(next) => !next && close()}
      title={created ? 'API 密钥已创建' : '创建 API 密钥'}
      dismissible={!mutation.isPending}
      footer={
        created ? (
          <Button onClick={close}>完成</Button>
        ) : (
          <>
            <Button type="button" variant="secondary" disabled={mutation.isPending} onClick={close}>
              取消
            </Button>
            <Button type="submit" form="key-form" disabled={mutation.isPending}>
              {mutation.isPending ? '创建中' : '创建'}
            </Button>
          </>
        )
      }
    >
      {created ? (
        <div className="one-time-result">
          <div className="secret-reveal">
            <code>{created.secret}</code>
            <Button
              variant="secondary"
              icon={copied ? <Check size={16} /> : <Copy size={16} />}
              onClick={() =>
                void navigator.clipboard
                  .writeText(`OPENAI_BASE_URL=${gatewayBaseURL}\nOPENAI_API_KEY=${created.secret}`)
                  .then(() => setCopied(true))
              }
            >
              {copied ? '已复制' : '复制调用配置'}
            </Button>
          </div>
          <dl className="fact-list">
            <div>
              <dt>Base URL</dt>
              <dd>
                <code>{gatewayBaseURL}</code>
              </dd>
            </div>
            <div>
              <dt>名称</dt>
              <dd>{created.key.name}</dd>
            </div>
            <div>
              <dt>模型</dt>
              <dd>{created.key.authorizedModels.join('、')}</dd>
            </div>
          </dl>
        </div>
      ) : (
        <form id="key-form" className="form-grid" onSubmit={submit}>
          {session.role === 'administrator' ? (
            <Field label="所属成员" htmlFor="key-owner">
              <NativeSelect
                id="key-owner"
                autoFocus
                value={ownerId}
                disabled={mutation.isPending}
                onChange={(event) => {
                  setOwnerId(event.target.value)
                  setModelIds([])
                }}
              >
                <option value="">请选择</option>
                {(members.data?.items ?? [])
                  .filter((member) => member.role === 'member')
                  .map((member) => (
                    <option key={member.id} value={member.id}>
                      {member.displayName} · {member.email}
                    </option>
                  ))}
              </NativeSelect>
            </Field>
          ) : (
            <Field label="所属成员" htmlFor="key-owner">
              <Input id="key-owner" autoFocus value={session.displayName} readOnly />
            </Field>
          )}
          <Field label="名称" htmlFor="key-name">
            <Input
              id="key-name"
              value={name}
              readOnly={mutation.isPending}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          <fieldset className="choice-field field--full">
            <legend>授权模型</legend>
            <div className="choice-grid">
              {authorizedModels.map((model) => (
                <label key={model.id}>
                  <input
                    type="checkbox"
                    checked={modelIds.includes(model.id)}
                    disabled={mutation.isPending}
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
            {!effectiveOwnerId ? (
              <p className="choice-field__empty">选择成员后显示活动订阅模型</p>
            ) : subscriptions.isLoading ? (
              <p className="choice-field__empty">正在读取活动订阅模型</p>
            ) : authorizedModels.length === 0 ? (
              <p className="choice-field__empty">该成员当前没有可用于新 API 密钥的活动订阅模型</p>
            ) : null}
          </fieldset>
          <Field label="到期时间" htmlFor="key-expiry" hint="留空表示跟随成员服务治理">
            <Input
              id="key-expiry"
              type="datetime-local"
              value={expiresAt}
              readOnly={mutation.isPending}
              onChange={(event) => setExpiresAt(event.target.value)}
            />
          </Field>
          <FormProblem error={mutation.error ?? members.error ?? subscriptions.error} />
        </form>
      )}
    </DialogFrame>
  )
}
