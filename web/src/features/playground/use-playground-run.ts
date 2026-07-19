import { useCallback, useRef, useState } from 'react'

import {
  ApiProblem,
  streamPlaygroundRun,
  type ApiProblemShape,
  type OperationPhase,
  type PlaygroundEvent,
  type PlaygroundMessage,
  type PlaygroundRunInput,
} from '@/api'

interface RunFacts {
  phase: OperationPhase | 'idle'
  step: string
  requestId?: string | undefined
  inputTokens?: number | undefined
  outputTokens?: number | undefined
  usageSource?: 'authoritative' | 'estimated' | undefined
  error?: ApiProblemShape | undefined
}

const idleFacts: RunFacts = { phase: 'idle', step: '等待提交' }

export function usePlaygroundRun() {
  const [messages, setMessages] = useState<PlaygroundMessage[]>([])
  const [facts, setFacts] = useState<RunFacts>(idleFacts)
  const controllerRef = useRef<AbortController | null>(null)

  const handleEvent = useCallback((assistantId: string, event: PlaygroundEvent) => {
    if (event.type === 'phase') {
      setFacts((current) => ({
        ...current,
        phase: event.phase,
        step: event.step,
        requestId: event.requestId,
      }))
      return
    }
    if (event.type === 'content' || event.type === 'reasoning') {
      setMessages((current) =>
        current.map((message) =>
          message.id === assistantId
            ? { ...message, [event.type]: `${message[event.type] ?? ''}${event.delta}` }
            : message,
        ),
      )
      return
    }
    if (event.type === 'tool_call') {
      setMessages((current) =>
        current.map((message) =>
          message.id === assistantId
            ? {
                ...message,
                toolCall: {
                  name: event.name || message.toolCall?.name || '',
                  arguments: `${message.toolCall?.arguments ?? ''}${event.argumentsDelta}`,
                },
              }
            : message,
        ),
      )
      return
    }
    if (event.type === 'usage') {
      setFacts((current) => ({
        ...current,
        inputTokens: event.inputTokens,
        outputTokens: event.outputTokens,
        usageSource: event.source,
      }))
      return
    }
    if (event.type === 'completed') {
      setFacts((current) => ({
        ...current,
        phase: 'completed',
        step: '响应完成',
        requestId: event.requestId,
      }))
      return
    }
    setFacts((current) => ({
      ...current,
      phase: 'failed',
      step: '请求失败',
      error: event.problem,
      requestId: event.problem.requestId ?? current.requestId,
    }))
  }, [])

  const run = useCallback(
    async (input: PlaygroundRunInput) => {
      controllerRef.current?.abort()
      const controller = new AbortController()
      controllerRef.current = controller
      const userMessage: PlaygroundMessage = {
        id: crypto.randomUUID(),
        role: 'user',
        content: input.messages.at(-1)?.content ?? '',
      }
      const assistant: PlaygroundMessage = {
        id: crypto.randomUUID(),
        role: 'assistant',
        content: '',
        reasoning: '',
      }
      setMessages((current) => [...current, userMessage, assistant])
      setFacts({ phase: 'submitted', step: '请求已提交' })
      try {
        await streamPlaygroundRun(input, controller.signal, (event) =>
          handleEvent(assistant.id, event),
        )
      } catch (error) {
        if (controller.signal.aborted) return
        const problem =
          error instanceof ApiProblem
            ? {
                status: error.status,
                code: error.code,
                message: error.message,
                retryable: error.retryable,
                ...(error.stage ? { stage: error.stage } : {}),
                ...(error.requestId ? { requestId: error.requestId } : {}),
              }
            : {
                status: 500,
                code: 'playground_failed',
                message: 'Playground 请求未完成',
                retryable: true,
              }
        setFacts((current) => ({ ...current, phase: 'failed', step: '请求失败', error: problem }))
      } finally {
        if (controllerRef.current === controller) controllerRef.current = null
      }
    },
    [handleEvent],
  )

  const cancel = useCallback(() => {
    controllerRef.current?.abort()
    controllerRef.current = null
    setFacts((current) => ({ ...current, phase: 'canceled', step: '请求已取消' }))
  }, [])

  const clear = useCallback(() => {
    controllerRef.current?.abort()
    controllerRef.current = null
    setMessages([])
    setFacts(idleFacts)
  }, [])

  return { messages, facts, run, cancel, clear }
}
