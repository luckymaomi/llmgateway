import { useCallback, useRef, useState } from 'react'

import {
  ApiProblem,
  streamGatewayKeyTest,
  type ApiProblemShape,
  type OperationPhase,
  type GatewayKeyTestEvent,
  type GatewayKeyTestInput,
} from '@/api'

export interface GatewayKeyTestFacts {
  phase: OperationPhase | 'idle'
  step: string
  requestId?: string
  responseText: string
  inputTokens?: number
  outputTokens?: number
  usageSource?: 'authoritative' | 'estimated'
  error?: ApiProblemShape
}

const idleFacts: GatewayKeyTestFacts = {
  phase: 'idle',
  step: '等待测试',
  responseText: '',
}

export function useGatewayKeyTestRun() {
  const [facts, setFacts] = useState<GatewayKeyTestFacts>(idleFacts)
  const controllerRef = useRef<AbortController | undefined>(undefined)

  const handleEvent = useCallback((event: GatewayKeyTestEvent) => {
    if (event.type === 'phase') {
      setFacts((current) => ({
        ...current,
        phase: event.phase,
        step: event.step,
        requestId: event.requestId,
      }))
      return
    }
    if (event.type === 'content') {
      setFacts((current) => ({
        ...current,
        responseText: `${current.responseText}${event.delta}`,
      }))
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
    if (event.type === 'error') {
      setFacts((current) => {
        const requestId = event.problem.requestId ?? current.requestId
        return {
          ...current,
          phase: 'failed',
          step: '请求失败',
          error: event.problem,
          ...(requestId ? { requestId } : {}),
        }
      })
    }
  }, [])

  const run = useCallback(
    async (input: GatewayKeyTestInput) => {
      controllerRef.current?.abort()
      const controller = new AbortController()
      controllerRef.current = controller
      setFacts({ phase: 'submitted', step: '请求已提交', responseText: '' })
      try {
        await streamGatewayKeyTest(input, controller.signal, handleEvent)
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
                code: 'request_test_failed',
                message: '测试请求未完成',
                retryable: true,
              }
        setFacts((current) => ({ ...current, phase: 'failed', step: '请求失败', error: problem }))
      } finally {
        if (controllerRef.current === controller) controllerRef.current = undefined
      }
    },
    [handleEvent],
  )

  const cancel = useCallback(() => {
    controllerRef.current?.abort()
    controllerRef.current = undefined
    setFacts((current) => ({
      ...current,
      phase: 'uncertain',
      step: '已停止等待，服务端结果待确认',
    }))
  }, [])

  return { facts, run, cancel }
}
