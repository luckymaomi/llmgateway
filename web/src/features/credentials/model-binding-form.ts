import { z } from 'zod'

export const modelBindingsSchema = z
  .array(
    z.object({
      modelId: z.string().uuid(),
      priority: z.number().int().min(0).max(1000),
      weight: z.number().int().min(1).max(1000),
    }),
  )
  .min(1, '请选择至少一个模型')
