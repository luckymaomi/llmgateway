import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { FormEvent } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { catalogApi, costingApi, type ModelPriceInput } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

const decimalPrice = /^(0|[1-9][0-9]{0,6})(\.[0-9]{1,9})?$/
const schema = z.object({
  modelId: z.string().uuid('请选择模型'),
  currency: z
    .string()
    .trim()
    .regex(/^[A-Za-z]{3}$/, '请输入三位币种代码'),
  inputPricePerMillionTokens: z
    .string()
    .trim()
    .regex(decimalPrice, '请输入最多 9 位小数的非负价格'),
  outputPricePerMillionTokens: z
    .string()
    .trim()
    .regex(decimalPrice, '请输入最多 9 位小数的非负价格'),
  effectiveAt: z.string().min(1, '请选择生效时间'),
})

type Values = z.infer<typeof schema>

export function PriceForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const models = useQuery({
    queryKey: ['models', 'price-form'],
    queryFn: ({ signal }) => catalogApi.models(signal),
    enabled: open,
  })
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: defaultValues() })
  const mutation = useMutation({
    mutationFn: ({ input, idempotencyKey }: { input: ModelPriceInput; idempotencyKey: string }) =>
      costingApi.createPrice(input, idempotencyKey),
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['model-prices'] })
      form.reset(defaultValues())
      onOpenChange(false)
    },
  })

  async function submit(values: Values): Promise<void> {
    try {
      await mutation.mutateAsync({
        idempotencyKey: crypto.randomUUID(),
        input: {
          modelId: values.modelId,
          currency: values.currency.toUpperCase(),
          inputPricePerMillionTokens: values.inputPricePerMillionTokens,
          outputPricePerMillionTokens: values.outputPricePerMillionTokens,
          effectiveAt: new Date(values.effectiveAt).toISOString(),
        },
      })
    } catch {
      // The mutation state renders the typed error.
    }
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    await form.handleSubmit(submit)(event)
  }

  return (
    <DialogFrame
      open={open}
      onOpenChange={(next) => {
        if (!mutation.isPending || next) onOpenChange(next)
      }}
      title="新增价格版本"
      dismissible={!mutation.isPending}
      footer={
        <>
          <Button
            type="button"
            variant="secondary"
            disabled={mutation.isPending}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button type="submit" form="price-form" disabled={mutation.isPending}>
            保存
          </Button>
        </>
      }
    >
      <form id="price-form" className="form-grid" onSubmit={(event) => void handleSubmit(event)}>
        <Field label="模型" htmlFor="price-model" error={form.formState.errors.modelId?.message}>
          <NativeSelect id="price-model" autoFocus {...form.register('modelId')}>
            <option value="">请选择</option>
            {models.data?.map((model) => (
              <option key={model.id} value={model.id}>
                {model.publicName}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field
          label="币种"
          htmlFor="price-currency"
          error={form.formState.errors.currency?.message}
        >
          <Input id="price-currency" maxLength={3} {...form.register('currency')} />
        </Field>
        <Field
          label="输入价格 / 百万 Token"
          htmlFor="price-input"
          error={form.formState.errors.inputPricePerMillionTokens?.message}
        >
          <Input
            id="price-input"
            inputMode="decimal"
            {...form.register('inputPricePerMillionTokens')}
          />
        </Field>
        <Field
          label="输出价格 / 百万 Token"
          htmlFor="price-output"
          error={form.formState.errors.outputPricePerMillionTokens?.message}
        >
          <Input
            id="price-output"
            inputMode="decimal"
            {...form.register('outputPricePerMillionTokens')}
          />
        </Field>
        <Field
          label="生效时间"
          htmlFor="price-effective"
          error={form.formState.errors.effectiveAt?.message}
        >
          <Input id="price-effective" type="datetime-local" {...form.register('effectiveAt')} />
        </Field>
        <FormProblem error={mutation.error ?? models.error} />
      </form>
    </DialogFrame>
  )
}

function defaultValues(): Values {
  const local = new Date(Date.now() - new Date().getTimezoneOffset() * 60_000)
    .toISOString()
    .slice(0, 16)
  return {
    modelId: '',
    currency: 'USD',
    inputPricePerMillionTokens: '',
    outputPricePerMillionTokens: '',
    effectiveAt: local,
  }
}
