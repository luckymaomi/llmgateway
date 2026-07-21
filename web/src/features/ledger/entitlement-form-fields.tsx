import type { UseFormReturn } from 'react-hook-form'

import type { ActiveConfigurationModel, UserAccount } from '@/api'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

import type { EntitlementValues } from './entitlement-form-contract'

export function EntitlementFormFields({
  form,
  controlsLocked,
  users,
  availableModels,
  uncertain,
  persistenceFailed,
  error,
}: {
  form: UseFormReturn<EntitlementValues>
  controlsLocked: boolean
  users: UserAccount[]
  availableModels: ActiveConfigurationModel[]
  uncertain: boolean
  persistenceFailed: boolean
  error: unknown
}) {
  return (
    <>
      <Field
        label="用户"
        htmlFor="entitlement-owner"
        error={form.formState.errors.ownerId?.message}
      >
        <NativeSelect
          id="entitlement-owner"
          autoFocus
          disabled={controlsLocked}
          {...form.register('ownerId')}
        >
          <option value="">请选择</option>
          {users.map((user) => (
            <option key={user.id} value={user.id}>
              {user.displayName}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <Field
        label="类型"
        htmlFor="entitlement-kind"
        error={form.formState.errors.planKind?.message}
      >
        <NativeSelect
          id="entitlement-kind"
          disabled={controlsLocked}
          {...form.register('planKind')}
        >
          <option value="token">Token Plan</option>
          <option value="coding">Coding Plan</option>
        </NativeSelect>
      </Field>
      <Field
        label="资源域"
        htmlFor="entitlement-domain"
        error={form.formState.errors.resourceDomain?.message}
      >
        <NativeSelect
          id="entitlement-domain"
          disabled={controlsLocked}
          {...form.register('resourceDomain')}
        >
          <option value="free">免费资源域</option>
          <option value="professional">专业资源域</option>
        </NativeSelect>
      </Field>
      <Field
        label="模型范围"
        htmlFor="entitlement-model"
        error={form.formState.errors.modelId?.message}
      >
        <NativeSelect
          id="entitlement-model"
          disabled={controlsLocked}
          {...form.register('modelId')}
        >
          <option value="">资源域内全部已发布模型</option>
          {availableModels.map((model) => (
            <option key={model.id} value={model.id}>
              {model.alias} · {model.providerName}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <Field
        label="Token 额度"
        htmlFor="entitlement-token"
        error={form.formState.errors.grantedTokens?.message}
      >
        <Input
          id="entitlement-token"
          type="number"
          min={1}
          readOnly={controlsLocked}
          {...form.register('grantedTokens', { valueAsNumber: true })}
        />
      </Field>
      <Field label="RPM" htmlFor="entitlement-rpm" error={form.formState.errors.rpmLimit?.message}>
        <Input
          id="entitlement-rpm"
          type="number"
          min={1}
          readOnly={controlsLocked}
          {...form.register('rpmLimit', { valueAsNumber: true })}
        />
      </Field>
      <Field label="TPM" htmlFor="entitlement-tpm" error={form.formState.errors.tpmLimit?.message}>
        <Input
          id="entitlement-tpm"
          type="number"
          min={1}
          readOnly={controlsLocked}
          {...form.register('tpmLimit', { valueAsNumber: true })}
        />
      </Field>
      <Field
        label="并发上限"
        htmlFor="entitlement-concurrency"
        error={form.formState.errors.concurrencyLimit?.message}
      >
        <Input
          id="entitlement-concurrency"
          type="number"
          min={1}
          readOnly={controlsLocked}
          {...form.register('concurrencyLimit', { valueAsNumber: true })}
        />
      </Field>
      <Field
        label="开始时间"
        htmlFor="entitlement-start"
        error={form.formState.errors.startsAt?.message}
      >
        <Input
          id="entitlement-start"
          type="datetime-local"
          readOnly={controlsLocked}
          {...form.register('startsAt')}
        />
      </Field>
      <Field
        label="到期时间"
        htmlFor="entitlement-expiry"
        error={form.formState.errors.expiresAt?.message}
      >
        <Input
          id="entitlement-expiry"
          type="datetime-local"
          readOnly={controlsLocked}
          {...form.register('expiresAt')}
        />
      </Field>
      <Field
        label="分配原因"
        htmlFor="entitlement-reason"
        error={form.formState.errors.reason?.message}
      >
        <Input id="entitlement-reason" readOnly={controlsLocked} {...form.register('reason')} />
      </Field>
      {uncertain ? (
        <div className="inline-problem" role="alert">
          分配结果暂时无法确认。请确认原操作，系统会使用同一幂等键对账。
        </div>
      ) : (
        <>
          {persistenceFailed ? (
            <div className="inline-problem" role="alert">
              浏览器无法保存待确认操作，本次未提交。请允许当前标签页使用会话存储后重试。
            </div>
          ) : null}
          <FormProblem error={error} />
        </>
      )}
    </>
  )
}
