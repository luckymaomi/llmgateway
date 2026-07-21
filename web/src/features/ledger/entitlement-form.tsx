import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useState, type FormEvent, type MouseEvent } from 'react'
import { useForm, useWatch } from 'react-hook-form'

import { accessApi, catalogApi, ledgerApi, type EntitlementInput } from '@/api'
import {
  clearPendingEntitlementOperation,
  storePendingEntitlementOperation,
} from '@/app/pending-operations'
import { useSession } from '@/app/session'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'

import {
  defaultEntitlementValues,
  entitlementSchema,
  entitlementValuesFromPending,
  isEntitlementOutcomeUnknown,
  readPendingEntitlementSubmission,
  type EntitlementSubmission,
  type EntitlementValues,
} from './entitlement-form-contract'
import { EntitlementFormFields } from './entitlement-form-fields'

export function EntitlementForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const session = useSession()
  const queryClient = useQueryClient()
  const [uncertain, setUncertain] = useState<EntitlementSubmission | undefined>(() =>
    readPendingEntitlementSubmission(session.userId),
  )
  const [persistenceFailed, setPersistenceFailed] = useState(false)
  const users = useQuery({
    queryKey: ['users', 'entitlement-form'],
    queryFn: ({ signal }) => accessApi.users({ page: 1, pageSize: 100, status: 'active' }, signal),
    enabled: open && session.role === 'administrator',
  })
  const activeConfiguration = useQuery({
    queryKey: ['configuration-active'],
    queryFn: ({ signal }) => catalogApi.activeConfiguration(signal),
    enabled: open && session.role === 'administrator',
  })
  const form = useForm<EntitlementValues>({
    resolver: zodResolver(entitlementSchema),
    defaultValues: entitlementValuesFromPending(uncertain),
  })
  const resourceDomain = useWatch({ control: form.control, name: 'resourceDomain' })
  const modelID = useWatch({ control: form.control, name: 'modelId' })
  const availableModels = useMemo(
    () =>
      (activeConfiguration.data?.models ?? []).filter(
        (model) => model.resourceDomain === resourceDomain,
      ),
    [activeConfiguration.data?.models, resourceDomain],
  )
  useEffect(() => {
    if (modelID && !availableModels.some((model) => model.id === modelID)) {
      form.setValue('modelId', '')
    }
  }, [availableModels, form, modelID])

  const mutation = useMutation({
    mutationFn: (submission: EntitlementSubmission) =>
      ledgerApi.createEntitlement(submission.input, submission.idempotencyKey),
    async onSuccess() {
      clearPendingEntitlementOperation(session.userId)
      setUncertain(undefined)
      form.reset(defaultEntitlementValues())
      await queryClient.invalidateQueries({ queryKey: ['entitlements'] })
      onOpenChange(false)
    },
    onError(error, submission) {
      if (isEntitlementOutcomeUnknown(error)) {
        setUncertain(submission)
      } else {
        clearPendingEntitlementOperation(session.userId)
        setUncertain(undefined)
      }
    },
  })

  function requestClose(): void {
    if (mutation.isPending || uncertain) return
    mutation.reset()
    setPersistenceFailed(false)
    form.reset(defaultEntitlementValues())
    onOpenChange(false)
  }

  async function submit(values: EntitlementValues): Promise<void> {
    if (uncertain) return
    const input: EntitlementInput = {
      ownerId: values.ownerId,
      planKind: values.planKind,
      resourceDomain: values.resourceDomain,
      grantedTokens: values.grantedTokens,
      concurrencyLimit: values.concurrencyLimit,
      startsAt: new Date(values.startsAt).toISOString(),
      expiresAt: new Date(values.expiresAt).toISOString(),
      reason: values.reason.trim(),
      ...(values.modelId ? { modelId: values.modelId } : {}),
      ...(values.rpmLimit !== undefined ? { rpmLimit: values.rpmLimit } : {}),
      ...(values.tpmLimit !== undefined ? { tpmLimit: values.tpmLimit } : {}),
    }
    const submission = { input, idempotencyKey: crypto.randomUUID() }
    if (!storePendingEntitlementOperation(session.userId, submission)) {
      setPersistenceFailed(true)
      return
    }
    setPersistenceFailed(false)
    try {
      await mutation.mutateAsync(submission)
    } catch {
      // The mutation state renders the typed error or recovery action.
    }
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    const element = event.currentTarget
    if (element.dataset.submissionPending === 'true') return
    element.dataset.submissionPending = 'true'
    try {
      await form.handleSubmit(submit)(event)
    } finally {
      delete element.dataset.submissionPending
    }
  }

  async function retryUncertain(event: MouseEvent<HTMLButtonElement>): Promise<void> {
    if (!uncertain || event.currentTarget.dataset.submissionPending === 'true') return
    const button = event.currentTarget
    button.dataset.submissionPending = 'true'
    try {
      await mutation.mutateAsync(uncertain)
    } catch {
      // The same recovery action remains available while the outcome is unknown.
    } finally {
      delete button.dataset.submissionPending
    }
  }

  if (session.role !== 'administrator') return null
  const controlsLocked = mutation.isPending || Boolean(uncertain)
  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) requestClose()
      }}
      title="分配额度或套餐"
      width="lg"
      dismissible={!controlsLocked}
      footer={
        <>
          <Button variant="secondary" disabled={controlsLocked} onClick={requestClose}>
            取消
          </Button>
          {uncertain ? (
            <Button disabled={mutation.isPending} onClick={(event) => void retryUncertain(event)}>
              {mutation.isPending ? '正在确认' : '确认原操作'}
            </Button>
          ) : (
            <Button type="submit" form="entitlement-form" disabled={mutation.isPending}>
              {mutation.isPending ? '分配中' : '分配'}
            </Button>
          )}
        </>
      }
    >
      <form
        id="entitlement-form"
        className="form-grid"
        onSubmit={(event) => void handleSubmit(event)}
      >
        <EntitlementFormFields
          form={form}
          controlsLocked={controlsLocked}
          users={users.data?.items ?? []}
          availableModels={availableModels}
          uncertain={Boolean(uncertain)}
          persistenceFailed={persistenceFailed}
          error={mutation.error ?? users.error ?? activeConfiguration.error}
        />
      </form>
    </DialogFrame>
  )
}
